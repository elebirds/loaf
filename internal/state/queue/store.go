// Package queuestate owns the private machine/user admission ledger.
// Transactions never span input copies, process starts or observations.
package queuestate

import (
    "context"
    "database/sql"
    "encoding/json"
    "errors"
    "net/url"
    "os"
    "path/filepath"
    "regexp"
    "slices"
    "syscall"
    "time"

    "github.com/elebirds/loaf/internal/event"
    "github.com/elebirds/loaf/internal/queue"
    "github.com/elebirds/loaf/internal/state/eventlog"
    _ "modernc.org/sqlite"
)

const SchemaVersion = 1
const applicationID = 0x4c514555
const schema = `
CREATE TABLE queue_owner(singleton INTEGER PRIMARY KEY CHECK(singleton=1),host_identity TEXT NOT NULL,host_binding TEXT NOT NULL,software_version TEXT NOT NULL,target_id TEXT NOT NULL UNIQUE,slots INTEGER NOT NULL CHECK(slots BETWEEN 1 AND 64),paused INTEGER NOT NULL CHECK(paused IN (0,1)));
CREATE TABLE queue_requests(id TEXT PRIMARY KEY,run_id TEXT NOT NULL UNIQUE,state TEXT NOT NULL,sequence INTEGER NOT NULL,public BLOB NOT NULL,execution BLOB NOT NULL,attempted INTEGER NOT NULL CHECK(attempted IN(0,1)));
CREATE UNIQUE INDEX queue_sequence ON queue_requests(sequence) WHERE sequence>0;
CREATE INDEX queue_state ON queue_requests(state,sequence);
PRAGMA application_id=1280394581;
PRAGMA user_version=1;
`

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,95}$`)

// Identity is the independently observed host/user identity and optional frozen
// cloud instance binding. Neither value is a credential or mutable alias.
type Identity struct{ Host, Binding, Version string }

// Store is one bounded SQLite connection; OS ownership locks remain external.
type Store struct {
    database *sql.DB
    Path     string
    TargetID string
}

// Entry retains the private frozen input separately from its public request.
type Entry struct {
    Request   queue.Request
    Execution queue.Execution
    Attempted bool
}

// Open creates only a fresh known ledger or verifies an existing one.
func Open(ctx context.Context, path string, identity Identity, write bool) (*Store, error) {
    if !filepath.IsAbs(path) || write && identity.Host == "" {
        return nil, errors.New("QUEUE_IDENTITY_MISMATCH")
    }
    if write {
        if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
            return nil, err
        }
    }
    dir, err := os.Lstat(filepath.Dir(path))
    if err != nil {
        return nil, err
    }
    if !dir.IsDir() || dir.Mode().Perm()&0077 != 0 {
        return nil, errors.New("QUEUE_UNSAFE_STATE")
    }
    if write {
        file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
        if err == nil {
            err = file.Close()
        }
        if err != nil && !errors.Is(err, os.ErrExist) {
            return nil, err
        }
    }
    for _, name := range []string{filepath.Dir(path), path, path + "-wal", path + "-shm"} {
        info, err := os.Lstat(name)
        if errors.Is(err, os.ErrNotExist) && (name == path+"-wal" || name == path+"-shm") {
            continue
        }
        if err != nil {
            return nil, err
        }
        owner, ok := info.Sys().(*syscall.Stat_t)
        if !ok || owner.Uid != uint32(os.Geteuid()) || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 || name != filepath.Dir(path) && !info.Mode().IsRegular() {
            return nil, errors.New("QUEUE_UNSAFE_STATE")
        }
    }
    u := url.URL{Scheme: "file", Path: path}
    mode := "ro"
    if write {
        mode = "rw"
    }
    q := url.Values{"mode": {mode}, "_pragma": {"busy_timeout(2000)", "foreign_keys(1)"}, "_txlock": {"immediate"}}
    u.RawQuery = q.Encode()
    database, err := sql.Open("sqlite", u.String())
    if err != nil {
        return nil, err
    }
    database.SetMaxOpenConns(1)
    s := &Store{database: database, Path: path}
    fail := func(err error) (*Store, error) { database.Close(); return nil, err }
    var version, app int
    if err = database.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
        return fail(err)
    }
    if err = database.QueryRowContext(ctx, "PRAGMA application_id").Scan(&app); err != nil {
        return fail(err)
    }
    if version == 0 && app == 0 && write {
        tx, err := database.BeginTx(ctx, nil)
        if err != nil {
            return fail(err)
        }
        var count int
        err = tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version)
        if err == nil && version == 0 {
            err = tx.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master").Scan(&count)
            if err == nil && count != 0 {
                err = errors.New("QUEUE_BAD_SCHEMA")
            }
            if err == nil {
                _, err = tx.ExecContext(ctx, schema+eventlog.Schema)
            }
            if err == nil {
                _, err = tx.ExecContext(ctx, "INSERT INTO queue_owner VALUES(1,?,?,?,'target_'||lower(hex(randomblob(12))),1,0)", identity.Host, identity.Binding, identity.Version)
            }
        }
        if err == nil {
            err = tx.Commit()
        } else {
            _ = tx.Rollback()
        }
        if err != nil {
            return fail(err)
        }
        if err = database.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
            return fail(err)
        }
        if err = database.QueryRowContext(ctx, "PRAGMA application_id").Scan(&app); err != nil {
            return fail(err)
        }
    }
    if version != SchemaVersion || app != applicationID {
        return fail(errors.New("QUEUE_BAD_SCHEMA"))
    }
    var savedHost, savedBinding, savedVersion string
    if err = database.QueryRowContext(ctx, "SELECT host_identity,host_binding,software_version,target_id FROM queue_owner WHERE singleton=1").Scan(&savedHost, &savedBinding, &savedVersion, &s.TargetID); err != nil {
        return fail(err)
    }
    if identity.Host != "" && savedHost != identity.Host || identity.Binding != "" && savedBinding != "" && identity.Binding != savedBinding {
        return fail(errors.New("QUEUE_IDENTITY_MISMATCH"))
    }
    if identity.Version != "" && savedVersion != identity.Version {
        return fail(errors.New("VERSION_MISMATCH"))
    }
    if write && savedBinding == "" && identity.Binding != "" {
        if _, err = database.ExecContext(ctx, "UPDATE queue_owner SET host_binding=? WHERE singleton=1 AND host_binding=''", identity.Binding); err != nil {
            return fail(err)
        }
    }
    if write {
        var journal string
        if err = database.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&journal); err != nil || journal != "wal" {
            return fail(errors.New("QUEUE_WRITE_FAILED"))
        }
        if _, err = database.ExecContext(ctx, "PRAGMA synchronous=FULL"); err != nil {
            return fail(err)
        }
    }
    if write {
        for _, name := range []string{filepath.Dir(path), filepath.Dir(filepath.Dir(path))} {
            directory, err := os.Open(name)
            if err != nil {
                return fail(err)
            }
            err = directory.Sync()
            directory.Close()
            if err != nil {
                return fail(err)
            }
        }
    }
    return s, nil
}

func (s *Store) Close() error { return s.database.Close() }

// Status observes configured and reserved capacity without checking GPU usage.
func (s *Store) Status(ctx context.Context) (queue.Status, error) {
    result := queue.Status{TargetID: s.TargetID}
    err := s.database.QueryRowContext(ctx, `SELECT slots,paused,(SELECT count(*) FROM queue_requests WHERE state IN('dispatching','unconfirmed','submitted')),(SELECT count(*) FROM queue_requests WHERE state='queued') FROM queue_owner WHERE singleton=1`).Scan(&result.Slots, &result.Paused, &result.Reserved, &result.Queued)
    return result, err
}

// Configure changes only future capacity admission; it cannot evict running work.
func (s *Store) Configure(ctx context.Context, slots int) (queue.Status, error) {
    if slots < 1 || slots > 64 {
        return queue.Status{}, errors.New("QUEUE_INVALID_SLOTS")
    }
    tx, err := s.database.BeginTx(ctx, nil)
    if err != nil {
        return queue.Status{}, err
    }
    defer tx.Rollback()
    var reserved int
    if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM queue_requests WHERE state IN('dispatching','unconfirmed','submitted')").Scan(&reserved); err != nil {
        return queue.Status{}, err
    }
    if slots < reserved {
        return queue.Status{}, errors.New("QUEUE_CAPACITY_BUSY")
    }
    if _, err = tx.ExecContext(ctx, "UPDATE queue_owner SET slots=? WHERE singleton=1", slots); err != nil {
        return queue.Status{}, err
    }
    if err = tx.Commit(); err != nil {
        return queue.Status{}, err
    }
    return s.Status(ctx)
}

func (s *Store) SetPaused(ctx context.Context, paused bool) (queue.Status, error) {
    _, err := s.database.ExecContext(ctx, "UPDATE queue_owner SET paused=? WHERE singleton=1", paused)
    if err != nil {
        return queue.Status{}, err
    }
    return s.Status(ctx)
}

// Prepare durably binds one request and Run before any source side effect.
func (s *Store) Prepare(ctx context.Context, r queue.Request, e queue.Execution) (Entry, error) {
    if r.FormatVersion != queue.Version || !idPattern.MatchString(r.ID) || !idPattern.MatchString(r.RunID) || !idPattern.MatchString(r.ProjectID) || r.TargetID != s.TargetID || r.CreatedAt.IsZero() || !filepath.IsAbs(e.Root) || e.Version == "" {
        return Entry{}, errors.New("QUEUE_BAD_REQUEST")
    }
    r.State = "preparing"
    r.Sequence = 0
    r.UpdatedAt = r.CreatedAt
    r.AttemptedAt = nil
    public, _ := json.Marshal(r)
    execution, err := json.Marshal(e)
    if err != nil || len(execution) > 900<<10 {
        return Entry{}, errors.New("QUEUE_BAD_REQUEST")
    }
    tx, err := s.database.BeginTx(ctx, nil)
    if err != nil {
        return Entry{}, err
    }
    defer tx.Rollback()
    existing, err := entry(tx.QueryRowContext(ctx, "SELECT public,execution,attempted FROM queue_requests WHERE id=?", r.ID))
    if err == nil {
        old, _ := json.Marshal(existing.Execution)
        if existing.Request.RunID != r.RunID || existing.Request.ProjectID != r.ProjectID || existing.Request.Name != r.Name || !slices.Equal(existing.Request.Tags, r.Tags) || !existing.Request.CreatedAt.Equal(r.CreatedAt) || string(old) != string(execution) {
            return Entry{}, errors.New("QUEUE_REQUEST_EXISTS")
        }
        return existing, nil
    }
    if !errors.Is(err, sql.ErrNoRows) {
        return Entry{}, err
    }
    _, err = tx.ExecContext(ctx, "INSERT INTO queue_requests VALUES(?,?,?,?,?,?,0)", r.ID, r.RunID, r.State, r.Sequence, public, execution)
    if err != nil {
        return Entry{}, err
    }
    if err = tx.Commit(); err != nil {
        return Entry{}, err
    }
    return Entry{Request: r, Execution: e}, nil
}

type row interface{ Scan(...any) error }

func entry(r row) (Entry, error) {
    var e Entry
    var public, private []byte
    if err := r.Scan(&public, &private, &e.Attempted); err != nil {
        return e, err
    }
    if json.Unmarshal(public, &e.Request) != nil || json.Unmarshal(private, &e.Execution) != nil || e.Request.FormatVersion != queue.Version {
        return Entry{}, errors.New("QUEUE_BAD_SCHEMA")
    }
    return e, nil
}

func (s *Store) Entry(ctx context.Context, id string) (Entry, error) {
    return entry(s.database.QueryRowContext(ctx, "SELECT public,execution,attempted FROM queue_requests WHERE id=?", id))
}
func (s *Store) Run(ctx context.Context, id string) (Entry, error) {
    return entry(s.database.QueryRowContext(ctx, "SELECT public,execution,attempted FROM queue_requests WHERE run_id=?", id))
}

// List returns live work before history, across project aliases. Active and
// queued requests retain FIFO order; preparations and history show recent work
// first. The boolean reports omitted rows without implying complete coverage.
func (s *Store) List(ctx context.Context, limit int) ([]Entry, bool, error) {
    if limit < 1 || limit > 1000 {
        return nil, false, errors.New("QUEUE_BAD_REQUEST")
    }
    rows, err := s.database.QueryContext(ctx, `SELECT public,execution,attempted FROM queue_requests ORDER BY
        CASE WHEN state IN('dispatching','unconfirmed','submitted') THEN 0 WHEN state='queued' THEN 1 WHEN state='preparing' THEN 2 ELSE 3 END,
        CASE WHEN state IN('dispatching','unconfirmed','submitted','queued') THEN sequence ELSE 0 END,
        julianday(json_extract(public,'$.updated_at')) DESC,id LIMIT ?`, limit+1)
    if err != nil {
        return nil, false, err
    }
    defer rows.Close()
    result := []Entry{}
    for rows.Next() {
        if len(result) == limit {
            return result, true, nil
        }
        e, err := entry(rows)
        if err != nil {
            return result, false, err
        }
        result = append(result, e)
    }
    return result, false, rows.Err()
}

func saveEntry(ctx context.Context, tx *sql.Tx, e Entry) error {
    b, err := json.Marshal(e.Request)
    if err != nil {
        return err
    }
    _, err = tx.ExecContext(ctx, "UPDATE queue_requests SET state=?,sequence=?,public=?,attempted=? WHERE id=?", e.Request.State, e.Request.Sequence, b, e.Attempted, e.Request.ID)
    return err
}

func appendEvent(ctx context.Context, tx *sql.Tx, e Entry, kind string) error {
    _, err := eventlog.Append(ctx, tx, e.Request.ID+":"+kind, event.Envelope{Type: kind, Subject: event.Subject{Kind: "queue_request", ID: e.Request.ID}, OccurredAt: e.Request.UpdatedAt, Correlation: event.Correlation{ProjectID: e.Request.ProjectID, TargetID: e.Request.TargetID, RunID: e.Request.RunID, QueueRequestID: e.Request.ID}, Data: event.Data{Status: e.Request.State, Code: e.Request.ReasonCode}})
    return err
}

// Admit confirms immutable prepared input and assigns a remote FIFO sequence.
// The caller verifies its prepared Run while holding the source/payload locks.
func (s *Store) Admit(ctx context.Context, id string) (Entry, error) {
    tx, err := s.database.BeginTx(ctx, nil)
    if err != nil {
        return Entry{}, err
    }
    defer tx.Rollback()
    e, err := entry(tx.QueryRowContext(ctx, "SELECT public,execution,attempted FROM queue_requests WHERE id=?", id))
    if err != nil {
        return e, err
    }
    if e.Request.State == "queued" {
        return e, nil
    }
    if e.Request.State != "preparing" {
        return e, errors.New("QUEUE_NOT_PREPARING")
    }
    if err = tx.QueryRowContext(ctx, "SELECT coalesce(max(sequence),0)+1 FROM queue_requests").Scan(&e.Request.Sequence); err != nil {
        return e, err
    }
    e.Request.State = "queued"
    e.Request.UpdatedAt = time.Now().UTC()
    if err = saveEntry(ctx, tx, e); err == nil {
        err = appendEvent(ctx, tx, e, "queue.enqueued")
    }
    if err != nil {
        return e, err
    }
    err = tx.Commit()
    return e, err
}

// Reserve validates the policy decision and records fixed Run reservations.
func (s *Store) Reserve(ctx context.Context, policy queue.Policy) ([]Entry, error) {
    tx, err := s.database.BeginTx(ctx, nil)
    if err != nil {
        return nil, err
    }
    defer tx.Rollback()
    var slots, reserved int
    var paused bool
    if err = tx.QueryRowContext(ctx, `SELECT slots,paused,(SELECT count(*) FROM queue_requests WHERE state IN('dispatching','unconfirmed','submitted')) FROM queue_owner WHERE singleton=1`).Scan(&slots, &paused, &reserved); err != nil {
        return nil, err
    }
    if paused || reserved >= slots {
        return []Entry{}, nil
    }
    rows, err := tx.QueryContext(ctx, "SELECT public,execution,attempted FROM queue_requests WHERE state='queued' ORDER BY sequence,id LIMIT 1000")
    if err != nil {
        return nil, err
    }
    entries := map[string]Entry{}
    candidates := []queue.Candidate{}
    for rows.Next() {
        e, err := entry(rows)
        if err != nil {
            rows.Close()
            return nil, err
        }
        entries[e.Request.ID] = e
        candidates = append(candidates, queue.Candidate{ID: e.Request.ID, Sequence: e.Request.Sequence})
    }
    err = rows.Err()
    rows.Close()
    if err != nil {
        return nil, err
    }
    ids, err := policy.Select(slices.Clone(candidates), slots-reserved)
    if err != nil {
        return nil, err
    }
    if err = queue.ValidateSelection(candidates, slots-reserved, ids); err != nil {
        return nil, err
    }
    selected := make([]Entry, 0, len(ids))
    for _, id := range ids {
        e := entries[id]
        e.Request.State = "dispatching"
        e.Request.UpdatedAt = time.Now().UTC()
        if err = saveEntry(ctx, tx, e); err != nil {
            return nil, err
        }
        selected = append(selected, e)
    }
    if err = tx.Commit(); err != nil {
        return nil, err
    }
    return selected, nil
}

// Active includes uncertain reservations so absence of a response never frees capacity.
func (s *Store) Active(ctx context.Context) ([]Entry, error) {
    rows, err := s.database.QueryContext(ctx, "SELECT public,execution,attempted FROM queue_requests WHERE state IN('dispatching','unconfirmed','submitted') ORDER BY sequence,id")
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    result := []Entry{}
    for rows.Next() {
        e, err := entry(rows)
        if err != nil {
            return result, err
        }
        result = append(result, e)
    }
    return result, rows.Err()
}

// MarkAttempted is durable before the single process-launch boundary.
func (s *Store) MarkAttempted(ctx context.Context, id string) (Entry, error) {
    tx, err := s.database.BeginTx(ctx, nil)
    if err != nil {
        return Entry{}, err
    }
    defer tx.Rollback()
    e, err := entry(tx.QueryRowContext(ctx, "SELECT public,execution,attempted FROM queue_requests WHERE id=?", id))
    if err != nil {
        return e, err
    }
    if e.Request.State != "dispatching" || e.Attempted {
        return e, errors.New("QUEUE_DISPATCH_ALREADY_ATTEMPTED")
    }
    now := time.Now().UTC()
    e.Attempted = true
    e.Request.AttemptedAt = &now
    e.Request.UpdatedAt = now
    if err = saveEntry(ctx, tx, e); err != nil {
        return e, err
    }
    err = tx.Commit()
    return e, err
}

// Confirm records actual acceptance or terminal completion of the original Run.
func (s *Store) Confirm(ctx context.Context, id string, terminal bool) (Entry, error) {
    return s.outcome(ctx, id, terminal, "")
}

// Unconfirmed retains the reservation and one actionable attention event.
func (s *Store) Unconfirmed(ctx context.Context, id, code string) (Entry, error) {
    return s.outcome(ctx, id, false, code)
}

func (s *Store) outcome(ctx context.Context, id string, terminal bool, code string) (Entry, error) {
    tx, err := s.database.BeginTx(ctx, nil)
    if err != nil {
        return Entry{}, err
    }
    defer tx.Rollback()
    e, err := entry(tx.QueryRowContext(ctx, "SELECT public,execution,attempted FROM queue_requests WHERE id=?", id))
    if err != nil {
        return e, err
    }
    switch e.Request.State {
    case "dispatching", "unconfirmed", "submitted":
    default:
        return e, errors.New("QUEUE_NOT_ACTIVE")
    }
    previousState, previousCode := e.Request.State, e.Request.ReasonCode
    kind := "queue.dispatched"
    e.Request.State = "submitted"
    e.Request.ReasonCode = ""
    if terminal {
        e.Request.State = "completed"
    }
    if code != "" {
        kind = "queue.attention_required"
        if previousState == "submitted" {
            e.Request.State = "submitted"
        } else {
            e.Request.State = "unconfirmed"
        }
        e.Request.ReasonCode = code
    }
    if e.Request.State == previousState && e.Request.ReasonCode == previousCode {
        return e, nil
    }
    e.Request.UpdatedAt = time.Now().UTC()
    if err = saveEntry(ctx, tx, e); err == nil {
        err = appendEvent(ctx, tx, e, kind)
    }
    if err != nil {
        return e, err
    }
    err = tx.Commit()
    return e, err
}

// Cancel is the durable no-start transition serialized with reservation.
func (s *Store) Cancel(ctx context.Context, id string) (Entry, error) {
    tx, err := s.database.BeginTx(ctx, nil)
    if err != nil {
        return Entry{}, err
    }
    defer tx.Rollback()
    e, err := entry(tx.QueryRowContext(ctx, "SELECT public,execution,attempted FROM queue_requests WHERE id=?", id))
    if err != nil {
        return e, err
    }
    if e.Request.State == "canceled" {
        return e, nil
    }
    if e.Request.State != "preparing" && e.Request.State != "queued" {
        return e, errors.New("QUEUE_NOT_CANCELABLE")
    }
    e.Request.State = "canceled"
    e.Request.UpdatedAt = time.Now().UTC()
    if err = saveEntry(ctx, tx, e); err == nil {
        err = appendEvent(ctx, tx, e, "queue.canceled")
    }
    if err != nil {
        return e, err
    }
    err = tx.Commit()
    return e, err
}

// Events reads the exact owning source stream without mutation.
func (s *Store) Events(ctx context.Context, in event.ReadInput) (event.Page, error) {
    return eventlog.Read(ctx, s.database, in)
}

// ErrorCode bounds persistence errors before they cross the remote boundary.
func ErrorCode(err error) error {
    if err == nil {
        return nil
    }
    if errors.Is(err, sql.ErrNoRows) {
        return errors.New("QUEUE_NOT_FOUND")
    }
    if errors.Is(err, os.ErrNotExist) {
        return errors.New("QUEUE_NOT_ENABLED")
    }
    switch err.Error() {
    case "BAD_FORMAT", "BAD_COMMAND", "BAD_ENV", "UNSAFE_PATH", "PROJECT_BUSY", "SYNC_INCOMPLETE", "RUN_NOT_FOUND", "RUN_EXISTS", "WRITE_FAILED", "CLEAN_BUSY", "CLEAN_UNSAFE", "VERSION_MISMATCH", "QUEUE_IDENTITY_MISMATCH", "QUEUE_UNSAFE_STATE", "QUEUE_BAD_SCHEMA", "QUEUE_WRITE_FAILED", "QUEUE_INVALID_SLOTS", "QUEUE_CAPACITY_BUSY", "QUEUE_BAD_REQUEST", "QUEUE_REQUEST_EXISTS", "QUEUE_NOT_PREPARING", "QUEUE_NOT_ACTIVE", "QUEUE_NOT_CANCELABLE", "QUEUE_DISPATCH_ALREADY_ATTEMPTED", "QUEUE_INPUT_NOT_PREPARED", "QUEUE_COORDINATOR_UNCONFIRMED", "QUEUE_CLEAN_UNCONFIRMED", "QUEUE_NO_START":
        return err
    }
    if errors.Is(err, event.ErrCursor) {
        return errors.New("EVENT_CURSOR_INVALID")
    }
    if errors.Is(err, event.ErrInvalid) {
        return errors.New("EVENT_INVALID")
    }
    if errors.Is(err, queue.ErrSelection) {
        return errors.New("QUEUE_INVALID_SELECTION")
    }
    return errors.New("QUEUE_WRITE_FAILED")
}
