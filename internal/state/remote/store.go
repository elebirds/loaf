// Package remotestate stores the private control state of one remote project.
// Payload directories and OS locks remain outside the database.
package remotestate

import (
    "context"
    "database/sql"
    "encoding/json"
    "errors"
    "fmt"
    "net/url"
    "os"
    "path/filepath"
    "syscall"
    "time"

    "github.com/elebirds/loaf/internal/event"
    "github.com/elebirds/loaf/internal/state/eventlog"
    "github.com/elebirds/loaf/internal/wire"
    "modernc.org/sqlite"
)

const Namespace = ".loaf-control-v2"
const SchemaVersion = 2
const applicationID = 0x4c4f4152

// Path returns the database path without touching the filesystem.
func Path(root string) string { return filepath.Join(root, Namespace, "state.sqlite3") }

// Store is a short-lived connection to one project's authoritative state.
// Callers hold the existing lifecycle locks, never a transaction across work.
type Store struct{ db *sql.DB }

const schema = `
CREATE TABLE ownership (singleton INTEGER PRIMARY KEY CHECK(singleton=1), project_id TEXT NOT NULL);
CREATE TABLE sync_state (singleton INTEGER PRIMARY KEY CHECK(singleton=1), complete INTEGER NOT NULL CHECK(complete IN (0,1)));
CREATE TABLE run_claims (run_id TEXT PRIMARY KEY, version TEXT NOT NULL, claimed_at TEXT NOT NULL);
CREATE TABLE runs (
    run_id TEXT PRIMARY KEY REFERENCES run_claims(run_id),
    format_version INTEGER NOT NULL, request TEXT NOT NULL,
    status TEXT NOT NULL, phase TEXT NOT NULL, created_at TEXT NOT NULL, completed_at TEXT,
    supervisor_pid INTEGER NOT NULL, supervisor_start TEXT NOT NULL, boot_id TEXT NOT NULL,
    child_pid INTEGER NOT NULL, exit_code INTEGER, signal TEXT NOT NULL, reason TEXT NOT NULL,
    copy_millis INTEGER NOT NULL, stop_grace_millis INTEGER
);
CREATE TABLE stop_requests (run_id TEXT PRIMARY KEY REFERENCES run_claims(run_id), grace_millis INTEGER NOT NULL CHECK(grace_millis BETWEEN 0 AND 300000));
CREATE TABLE cleanup_receipts (
    run_id TEXT PRIMARY KEY REFERENCES run_claims(run_id), version TEXT NOT NULL,
    root TEXT NOT NULL, project_id TEXT NOT NULL, summary TEXT NOT NULL,
    cleaned_at TEXT, device TEXT NOT NULL, inode TEXT NOT NULL
);
` + eventlog.Schema + `
PRAGMA application_id=1280262482;
PRAGMA user_version=2;`

// Initialize claims an empty project directory. Existing projects are verified,
// never imported or converted from another format.
func Initialize(root, project string) error {
    if _, err := os.Lstat(Path(root)); err == nil {
        s, err := Open(root, false)
        if err != nil {
            return err
        }
        defer s.Close()
        return s.VerifyProject(project)
    } else if !errors.Is(err, os.ErrNotExist) {
        return errors.New("WRITE_FAILED")
    }
    entries, err := os.ReadDir(root)
    if err != nil {
        return errors.New("WRITE_FAILED")
    }
    if len(entries) != 0 {
        return errors.New("PROJECT_UNOWNED")
    }
    dir := filepath.Join(root, Namespace)
    if err := os.Mkdir(dir, 0700); err != nil {
        return errors.New("PROJECT_BUSY")
    }
    file, err := os.OpenFile(Path(root), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
    if err != nil {
        return errors.New("WRITE_FAILED")
    }
    if err := file.Close(); err != nil {
        return errors.New("WRITE_FAILED")
    }
    s, err := open(root, true, true)
    if err != nil {
        return err
    }
    defer s.Close()
    tx, err := s.db.Begin()
    if err != nil {
        return errors.New("WRITE_FAILED")
    }
    defer tx.Rollback()
    if _, err := tx.Exec(schema); err != nil {
        return errors.New("WRITE_FAILED")
    }
    if _, err := tx.Exec("INSERT INTO ownership VALUES (1, ?)", project); err != nil {
        return errors.New("WRITE_FAILED")
    }
    if err := tx.Commit(); err != nil {
        return errors.New("WRITE_FAILED")
    }
    // The directory entry is part of the durable initial ownership claim.
    for _, name := range []string{dir, root} {
        f, err := os.Open(name)
        if err != nil {
            return errors.New("WRITE_FAILED")
        }
        err = f.Sync()
        f.Close()
        if err != nil {
            return errors.New("WRITE_FAILED")
        }
    }
    return nil
}

// Open never creates a project or database, even for a writable connection.
func Open(root string, writable bool) (*Store, error) { return open(root, writable, false) }

func open(root string, writable, fresh bool) (*Store, error) {
    for _, name := range []string{filepath.Join(root, Namespace), Path(root), Path(root) + "-wal", Path(root) + "-shm"} {
        info, err := os.Lstat(name)
        if errors.Is(err, os.ErrNotExist) && (name == Path(root)+"-wal" || name == Path(root)+"-shm") {
            continue
        }
        if err != nil {
            return nil, errors.New("PROJECT_MISMATCH")
        }
        stat, ok := info.Sys().(*syscall.Stat_t)
        if !ok || stat.Uid != uint32(os.Geteuid()) {
            return nil, errors.New("BAD_FORMAT")
        }
        if info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 || (name != filepath.Join(root, Namespace) && !info.Mode().IsRegular()) || (name == filepath.Join(root, Namespace) && !info.IsDir()) {
            return nil, errors.New("BAD_FORMAT")
        }
    }
    mode := "ro"
    if writable {
        mode = "rw"
    }
    dsn := (&url.URL{Scheme: "file", Path: Path(root), RawQuery: "mode=" + mode + "&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_txlock=immediate"}).String()
    db, err := sql.Open("sqlite", dsn)
    if err != nil {
        return nil, errors.New("BAD_FORMAT")
    }
    db.SetMaxOpenConns(1)
    fail := func(code string) (*Store, error) { db.Close(); return nil, errors.New(code) }
    var version, app int
    if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
        return fail("BAD_FORMAT")
    }
    if err := db.QueryRow("PRAGMA application_id").Scan(&app); err != nil {
        return fail("BAD_FORMAT")
    }
    if (!fresh && (version != SchemaVersion || app != applicationID)) || (fresh && (version != 0 || app != 0)) {
        return fail("BAD_FORMAT")
    }
    if writable {
        var journal string
        if db.QueryRow("PRAGMA journal_mode=WAL").Scan(&journal) != nil || journal != "wal" {
            return fail("WRITE_FAILED")
        }
        if _, err := db.Exec("PRAGMA synchronous=FULL"); err != nil {
            return fail("WRITE_FAILED")
        }
        // Keep WAL sidecars after the last writer closes so readers never need
        // to initialize them; their files inherit the database's private mode.
        conn, err := db.Conn(context.Background())
        if err != nil {
            return fail("WRITE_FAILED")
        }
        err = conn.Raw(func(raw any) error { _, err := raw.(sqlite.FileControl).FileControlPersistWAL("main", 1); return err })
        conn.Close()
        if err != nil {
            return fail("WRITE_FAILED")
        }
    } else if _, err := db.Exec("PRAGMA query_only=ON"); err != nil {
        return fail("BAD_FORMAT")
    }
    return &Store{db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) VerifyProject(project string) error {
    var actual string
    if s.db.QueryRow("SELECT project_id FROM ownership WHERE singleton=1").Scan(&actual) != nil {
        return errors.New("BAD_FORMAT")
    }
    if actual != project {
        return errors.New("PROJECT_MISMATCH")
    }
    return nil
}

func (s *Store) Sync() (*bool, error) {
    var complete bool
    err := s.db.QueryRow("SELECT complete FROM sync_state WHERE singleton=1").Scan(&complete)
    if errors.Is(err, sql.ErrNoRows) {
        return nil, nil
    }
    if err != nil {
        return nil, errors.New("BAD_FORMAT")
    }
    return &complete, nil
}

func (s *Store) SetSync(complete bool) error {
    _, err := s.db.Exec("INSERT INTO sync_state VALUES (1, ?) ON CONFLICT(singleton) DO UPDATE SET complete=excluded.complete", complete)
    if err != nil {
        return errors.New("WRITE_FAILED")
    }
    return nil
}

const runColumns = "format_version,request,status,phase,created_at,completed_at,supervisor_pid,supervisor_start,boot_id,child_pid,exit_code,signal,reason,copy_millis,stop_grace_millis"

// Run returns authoritative execution state; callers may derive a live observation
// without writing it back over the supervisor's record.
func (s *Store) Run(id string) (*wire.RunRecord, error) {
    var r wire.RunRecord
    var request, created string
    var completed sql.NullString
    err := s.db.QueryRow("SELECT "+runColumns+" FROM runs WHERE run_id=?", id).Scan(&r.FormatVersion, &request, &r.Status, &r.Phase, &created, &completed, &r.SupervisorPID, &r.SupervisorStart, &r.BootID, &r.ChildPID, &r.ExitCode, &r.Signal, &r.Reason, &r.CopyMillis, &r.StopGraceMillis)
    if errors.Is(err, sql.ErrNoRows) {
        return nil, errors.New("RUN_NOT_FOUND")
    }
    if err != nil || r.FormatVersion != wire.FormatVersion || json.Unmarshal([]byte(request), &r.Request) != nil {
        return nil, errors.New("BAD_FORMAT")
    }
    if r.Request.Version != wire.Version {
        return nil, errors.New("VERSION_MISMATCH")
    }
    if r.Request.RunID != id {
        return nil, errors.New("BAD_FORMAT")
    }
    r.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
    if err != nil {
        return nil, errors.New("BAD_FORMAT")
    }
    if completed.Valid {
        t, err := time.Parse(time.RFC3339Nano, completed.String)
        if err != nil {
            return nil, errors.New("BAD_FORMAT")
        }
        r.CompletedAt = &t
    }
    return &r, nil
}

func timeValue(t *time.Time) any {
    if t == nil {
        return nil
    }
    return t.UTC().Format(time.RFC3339Nano)
}

func runValues(r *wire.RunRecord) ([]any, error) {
    request, err := json.Marshal(r.Request)
    if err != nil {
        return nil, err
    }
    return []any{r.Request.RunID, r.FormatVersion, string(request), r.Status, r.Phase, r.CreatedAt.UTC().Format(time.RFC3339Nano), timeValue(r.CompletedAt), r.SupervisorPID, r.SupervisorStart, r.BootID, r.ChildPID, r.ExitCode, r.Signal, r.Reason, r.CopyMillis, r.StopGraceMillis}, nil
}

// CreateRun atomically claims an ID and publishes the copy intent before any
// payload side effect. A retained claim is never reused, including after cleanup.
func (s *Store) CreateRun(r *wire.RunRecord) error {
    values, err := runValues(r)
    if err != nil {
        return errors.New("WRITE_FAILED")
    }
    tx, err := s.db.Begin()
    if err != nil {
        return errors.New("WRITE_FAILED")
    }
    defer tx.Rollback()
    result, err := tx.Exec("INSERT INTO run_claims VALUES(?,?,?) ON CONFLICT(run_id) DO NOTHING", r.Request.RunID, r.Request.Version, r.CreatedAt.UTC().Format(time.RFC3339Nano))
    if err != nil {
        return errors.New("WRITE_FAILED")
    }
    count, err := result.RowsAffected()
    if err != nil {
        return errors.New("WRITE_FAILED")
    }
    if count == 0 {
        return errors.New("RUN_EXISTS")
    }
    if _, err := tx.Exec("INSERT INTO runs(run_id,"+runColumns+") VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)", values...); err != nil {
        return errors.New("WRITE_FAILED")
    }
    if err := tx.Commit(); err != nil {
        return errors.New("WRITE_FAILED")
    }
    return nil
}

// SaveRun is called by the execution owner while its OS lifecycle lock is held.
// It cannot recreate a cleaned record or regress a published terminal outcome.
func (s *Store) SaveRun(r *wire.RunRecord) error {
    values, err := runValues(r)
    if err != nil {
        return errors.New("WRITE_FAILED")
    }
    args := append(values[1:], r.Request.RunID)
    tx, err := s.db.Begin()
    if err != nil {
        return errors.New("WRITE_FAILED")
    }
    defer tx.Rollback()
    result, err := tx.Exec(`UPDATE runs SET format_version=?,request=?,status=?,phase=?,created_at=?,completed_at=?,supervisor_pid=?,supervisor_start=?,boot_id=?,child_pid=?,exit_code=?,signal=?,reason=?,copy_millis=?,stop_grace_millis=? WHERE run_id=? AND status NOT IN ('succeeded','failed','stopped')`, args...)
    if err != nil {
        return errors.New("WRITE_FAILED")
    }
    n, err := result.RowsAffected()
    if err != nil {
        return errors.New("WRITE_FAILED")
    }
    if n == 0 {
        return errors.New("RUN_EXISTS")
    }
    fact := event.Envelope{Subject: event.Subject{Kind: "run", ID: r.Request.RunID}, Correlation: event.Correlation{ProjectID: r.Request.ProjectID, RunID: r.Request.RunID, QueueRequestID: r.Request.QueueRequestID}, Data: event.Data{Phase: r.Phase}}
    if r.SupervisorPID > 0 {
        fact.Type = "run.accepted"
        fact.Data.Status = "accepted"
        if _, err := eventlog.Append(context.Background(), tx, "run:"+r.Request.RunID+":accepted", fact); err != nil {
            return errors.New("WRITE_FAILED")
        }
    }
    if wire.Terminal(r.Status) {
        fact.Type = "run." + r.Status
        fact.Data.Status = r.Status
        fact.Data.ExitCode = r.ExitCode
        fact.Data.Signal = r.Signal
        if r.CompletedAt != nil {
            fact.OccurredAt = *r.CompletedAt
        }
        if _, err := eventlog.Append(context.Background(), tx, "run:"+r.Request.RunID+":terminal", fact); err != nil {
            return errors.New("WRITE_FAILED")
        }
    }
    if err := tx.Commit(); err != nil {
        return errors.New("WRITE_FAILED")
    }
    return nil
}

// Events reads source facts without creating a project or touching payloads.
func (s *Store) Events(ctx context.Context, in event.ReadInput) (event.Page, error) {
    return eventlog.Read(ctx, s.db, in)
}

func (s *Store) Stop(id string) (*int64, error) {
    var grace int64
    err := s.db.QueryRow("SELECT grace_millis FROM stop_requests WHERE run_id=?", id).Scan(&grace)
    if errors.Is(err, sql.ErrNoRows) {
        return nil, nil
    }
    if err != nil || grace < 0 || grace > wire.MaxStopGrace.Milliseconds() {
        return nil, errors.New("BAD_FORMAT")
    }
    return &grace, nil
}

// AcceptStop keeps the first accepted policy. The caller holds the control lock
// shared with phase admission and final publication.
func (s *Store) AcceptStop(id string, grace int64) error {
    _, err := s.db.Exec("INSERT INTO stop_requests VALUES(?,?) ON CONFLICT(run_id) DO NOTHING", id, grace)
    if err != nil {
        return errors.New("WRITE_FAILED")
    }
    return nil
}

func (s *Store) Cleanup(id string) (*wire.CleanupReceipt, error) {
    r := &wire.CleanupReceipt{FormatVersion: 1, RunID: id}
    var summary, device, inode string
    var cleaned sql.NullString
    err := s.db.QueryRow("SELECT version,root,project_id,summary,cleaned_at,device,inode FROM cleanup_receipts WHERE run_id=?", id).Scan(&r.Version, &r.Root, &r.ProjectID, &summary, &cleaned, &device, &inode)
    if errors.Is(err, sql.ErrNoRows) {
        return nil, nil
    }
    if err != nil || json.Unmarshal([]byte(summary), &r.Summary) != nil || !r.Summary.Valid() {
        return nil, errors.New("BAD_FORMAT")
    }
    if _, err := fmt.Sscan(device, &r.Device); err != nil {
        return nil, errors.New("BAD_FORMAT")
    }
    if _, err := fmt.Sscan(inode, &r.Inode); err != nil || r.Inode == 0 {
        return nil, errors.New("BAD_FORMAT")
    }
    if r.Version != wire.Version {
        return nil, errors.New("VERSION_MISMATCH")
    }
    if cleaned.Valid {
        t, err := time.Parse(time.RFC3339Nano, cleaned.String)
        if err != nil {
            return nil, errors.New("BAD_FORMAT")
        }
        r.CleanedAt = &t
    }
    return r, nil
}

// SaveCleanup publishes the deletion tombstone and removes private execution
// metadata in one transaction. Payload deletion happens afterwards, outside SQL.
func (s *Store) SaveCleanup(r *wire.CleanupReceipt) error {
    summary, err := json.Marshal(r.Summary)
    if err != nil {
        return errors.New("CLEAN_UNCONFIRMED")
    }
    tx, err := s.db.Begin()
    if err != nil {
        return errors.New("CLEAN_UNCONFIRMED")
    }
    defer tx.Rollback()
    _, err = tx.Exec(`INSERT INTO cleanup_receipts VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(run_id) DO UPDATE SET cleaned_at=COALESCE(cleanup_receipts.cleaned_at,excluded.cleaned_at)`, r.RunID, r.Version, r.Root, r.ProjectID, string(summary), timeValue(r.CleanedAt), fmt.Sprint(r.Device), fmt.Sprint(r.Inode))
    if err != nil {
        return errors.New("CLEAN_UNCONFIRMED")
    }
    for _, table := range []string{"stop_requests", "runs"} {
        if _, err := tx.Exec("DELETE FROM "+table+" WHERE run_id=?", r.RunID); err != nil {
            return errors.New("CLEAN_UNCONFIRMED")
        }
    }
    if err := tx.Commit(); err != nil {
        return errors.New("CLEAN_UNCONFIRMED")
    }
    return nil
}
