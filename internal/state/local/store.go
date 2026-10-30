// Package local persists project choices and client-side run facts in SQLite.
package local

import (
    "context"
    "crypto/rand"
    "database/sql"
    "encoding/hex"
    "encoding/json"
    "errors"
    "os"
    "path/filepath"
    "reflect"
    "slices"
    "time"

    "github.com/elebirds/loaf/internal/config"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/state/db"
    "github.com/elebirds/loaf/internal/task"
    "github.com/elebirds/loaf/internal/wire"
)

// Store owns local state, independently of TOML configuration and credentials.
type Store struct{ Dir, Path string }

// New chooses the current state namespace without creating any files.
func New(dir string) *Store {
    return &Store{Dir: dir, Path: filepath.Join(dir, "state-v3", "state.db")}
}

// Existing reads a saved personal project choice, or an empty choice.
func (s *Store) Existing(root string) (config.Project, error) {
    var p config.Project
    database, err := db.Open(context.Background(), s.Path, false)
    if os.IsNotExist(err) {
        return p, nil
    }
    if err != nil {
        return p, stateError(err)
    }
    defer database.Close()
    err = database.QueryRow("SELECT id,root,target FROM projects WHERE root=?", root).Scan(&p.ID, &p.Root, &p.Target)
    if errors.Is(err, sql.ErrNoRows) {
        return p, nil
    }
    return p, stateError(err)
}

// Select atomically establishes the stable project identity and optional target.
func (s *Store) Select(ctx context.Context, root, target string) (config.Project, error) {
    var p config.Project
    if !filepath.IsAbs(root) || (target != "" && !config.ValidName(target)) {
        return p, fault.New("BAD_CONFIG", "invalid_target", nil)
    }
    database, err := db.Open(ctx, s.Path, true)
    if err != nil {
        return p, stateError(err)
    }
    defer database.Close()
    tx, err := database.BeginTx(ctx, nil)
    if err != nil {
        return p, stateError(err)
    }
    defer tx.Rollback()
    var id [12]byte
    if _, err = rand.Read(id[:]); err != nil {
        return p, err
    }
    _, err = tx.ExecContext(ctx, "INSERT INTO projects(root,id,target) VALUES(?,?,?) ON CONFLICT(root) DO NOTHING", root, "p_"+hex.EncodeToString(id[:]), target)
    if err == nil && target != "" {
        _, err = tx.ExecContext(ctx, "UPDATE projects SET target=? WHERE root=?", target, root)
    }
    if err == nil {
        err = tx.QueryRowContext(ctx, "SELECT id,root,target FROM projects WHERE root=?", root).Scan(&p.ID, &p.Root, &p.Target)
    }
    if err == nil {
        err = tx.Commit()
    }
    return p, stateError(err)
}

func stateError(err error) error {
    if err == nil {
        return nil
    }
    var f *fault.Error
    if errors.As(err, &f) {
        return err
    }
    return fault.Wrap("BAD_CONFIG", "run_state", err)
}

func loadRun(ctx context.Context, q interface {
    QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string) (task.RunRef, error) {
    return scanRun(q.QueryRowContext(ctx, "SELECT id,project_id,submission,attempted_at,data FROM runs WHERE id=?", id).Scan)
}

func scanRun(scan func(...any) error) (task.RunRef, error) {
    var id, projectID, submission string
    var attempted sql.NullString
    var data []byte
    var ref task.RunRef
    if err := scan(&id, &projectID, &submission, &attempted, &data); err != nil {
        return ref, err
    }
    if json.Unmarshal(data, &ref) != nil || !task.ValidRun(ref) ||
        ref.ID != id || ref.ProjectID != projectID || ref.Submission != submission || attempted.Valid != (ref.AttemptedAt != nil) {
        return task.RunRef{}, fault.New("BAD_CONFIG", "run_state", nil)
    }
    if attempted.Valid {
        stamp, err := time.Parse(time.RFC3339Nano, attempted.String)
        if err != nil || !stamp.Equal(*ref.AttemptedAt) {
            return task.RunRef{}, fault.New("BAD_CONFIG", "run_state", nil)
        }
    }
    return ref, nil
}

func sameBinding(a, b task.RunRef) bool {
    return a.ProjectID == b.ProjectID && a.RemoteDir == b.RemoteDir && a.Runner == b.Runner && a.Target == b.Target && a.Version == b.Version && a.QueueRequestID == b.QueueRequestID && reflect.DeepEqual(a.Connection, b.Connection) && slices.Equal(a.Outputs, b.Outputs)
}

// SaveRun commits the locator and submission intent before remote side effects.
func (s *Store) SaveRun(ctx context.Context, ref task.RunRef) error {
    if !task.ValidRun(ref) {
        return fault.New("BAD_CONFIG", "run_state", nil)
    }
    database, err := db.Open(ctx, s.Path, true)
    if err != nil {
        return stateError(err)
    }
    defer database.Close()
    tx, err := database.BeginTx(ctx, nil)
    if err != nil {
        return stateError(err)
    }
    defer tx.Rollback()
    old, err := loadRun(ctx, tx, ref.ID)
    if err == nil {
        if !sameBinding(old, ref) {
            return stateError(errors.New("RUN_REFERENCE_CONFLICT"))
        }
        // A confirmed rejection may retire an uncertain attempt back to
        // preparation. Accepted runs and cleanup receipts cannot be withdrawn.
        if old.Cleanup != nil || old.Abandonment != nil && old.Abandonment.NoStartAt != nil || old.Submission == "submitted" && ref.Submission != "submitted" {
            ref = old
        } else if old.Abandonment != nil {
            // An intent is not no-start proof. A concurrent confirmed launch
            // may win and retire that intent; a proven fence above never does.
            if ref.Submission == "submitted" {
                ref.Abandonment = nil
            } else {
                ref.Abandonment = old.Abandonment
            }
        }
    } else if !errors.Is(err, sql.ErrNoRows) {
        return stateError(err)
    }
    data, err := json.Marshal(ref)
    if err != nil {
        return err
    }
    var attempted any
    if ref.AttemptedAt != nil {
        attempted = ref.AttemptedAt.UTC().Format("2006-01-02T15:04:05.000000000Z")
    }
    _, err = tx.ExecContext(ctx, `INSERT INTO runs(id,project_id,attempted_at,submission,data) VALUES(?,?,?,?,?)
        ON CONFLICT(id) DO UPDATE SET attempted_at=excluded.attempted_at,submission=excluded.submission,data=excluded.data`, ref.ID, ref.ProjectID, attempted, ref.Submission, data)
    if err == nil {
        err = tx.Commit()
    }
    return stateError(err)
}

// Run reads one explicit project-scoped identity, including preparation attempts.
// Latest/history eligibility does not hide a saved identity from recovery.
func (s *Store) Run(projectID, id string) (task.RunRef, error) {
    database, err := db.Open(context.Background(), s.Path, false)
    if os.IsNotExist(err) {
        return task.RunRef{}, fault.New("RUN_NOT_FOUND", "run_not_found", nil)
    }
    if err != nil {
        return task.RunRef{}, stateError(err)
    }
    defer database.Close()
    ref, err := scanRun(database.QueryRow("SELECT id,project_id,submission,attempted_at,data FROM runs WHERE project_id=? AND id=?", projectID, id).Scan)
    if errors.Is(err, sql.ErrNoRows) {
        return task.RunRef{}, fault.New("RUN_NOT_FOUND", "run_not_found", nil)
    }
    return ref, stateError(err)
}

// Runs returns one project's submitted or uncertain locators, latest first.
func (s *Store) Runs(projectID string) ([]task.RunRef, error) {
    result := []task.RunRef{}
    database, err := db.Open(context.Background(), s.Path, false)
    if os.IsNotExist(err) {
        return result, nil
    }
    if err != nil {
        return nil, stateError(err)
    }
    defer database.Close()
    rows, err := database.Query("SELECT id,project_id,submission,attempted_at,data FROM runs WHERE project_id=? AND submission!='preparing' ORDER BY attempted_at DESC,id", projectID)
    if err != nil {
        return nil, stateError(err)
    }
    defer rows.Close()
    for rows.Next() {
        ref, err := scanRun(rows.Scan)
        if err != nil {
            return nil, stateError(err)
        }
        result = append(result, ref)
    }
    return result, stateError(rows.Err())
}

// RecordCleanup changes only retained availability; late replies cannot undo deletion.
func (s *Store) RecordCleanup(ctx context.Context, ref task.RunRef, cleanup *task.RunCleanup) error {
    if !task.ValidRun(ref) || cleanup != nil && (cleanup.RequestedAt.IsZero() || !cleanup.Summary.Valid()) {
        return fault.New("BAD_CONFIG", "run_state", nil)
    }
    database, err := db.Open(ctx, s.Path, true)
    if err != nil {
        return stateError(err)
    }
    defer database.Close()
    tx, err := database.BeginTx(ctx, nil)
    if err != nil {
        return stateError(err)
    }
    defer tx.Rollback()
    current, err := loadRun(ctx, tx, ref.ID)
    if err != nil {
        return stateError(err)
    }
    if current.Submission != "submitted" || !sameBinding(current, ref) {
        return stateError(errors.New("RUN_REFERENCE_CONFLICT"))
    }
    if current.Cleanup != nil && current.Cleanup.CleanedAt != nil {
        return nil
    }
    if cleanup == nil && (ref.Cleanup == nil || current.Cleanup == nil || !current.Cleanup.RequestedAt.Equal(ref.Cleanup.RequestedAt)) {
        return nil
    }
    if cleanup != nil && cleanup.CleanedAt == nil && current.Cleanup != nil {
        return nil
    }
    current.Cleanup = cleanup
    data, err := json.Marshal(current)
    if err != nil {
        return err
    }
    _, err = tx.ExecContext(ctx, "UPDATE runs SET data=? WHERE id=?", data, ref.ID)
    if err == nil {
        err = tx.Commit()
    }
    return stateError(err)
}

// RunMetadata reads immutable navigation labels separately from run identity.
func (s *Store) RunMetadata(projectID, id string) (task.RunMetadata, error) {
    var out task.RunMetadata
    err := s.readAux("run_metadata", projectID, id, &out)
    return out, err
}

func (s *Store) readAux(table, projectID, id string, out any) error {
    database, err := db.Open(context.Background(), s.Path, false)
    if os.IsNotExist(err) {
        return nil
    }
    if err != nil {
        return stateError(err)
    }
    defer database.Close()
    var data []byte
    // table is an internal constant, never caller-controlled SQL input.
    err = database.QueryRow("SELECT data FROM "+table+" WHERE project_id=? AND run_id=?", projectID, id).Scan(&data)
    if errors.Is(err, sql.ErrNoRows) {
        return nil
    }
    if err == nil {
        err = json.Unmarshal(data, out)
    }
    return stateError(err)
}

// SaveRunMetadata binds normalized labels once, before submission.
func (s *Store) SaveRunMetadata(ctx context.Context, projectID, id string, next task.RunMetadata) error {
    next, err := task.NormalizeRunMetadata(next)
    if err != nil {
        return err
    }
    database, err := db.Open(ctx, s.Path, true)
    if err != nil {
        return stateError(err)
    }
    defer database.Close()
    tx, err := database.BeginTx(ctx, nil)
    if err != nil {
        return stateError(err)
    }
    defer tx.Rollback()
    ref, err := loadRun(ctx, tx, id)
    if errors.Is(err, sql.ErrNoRows) || err == nil && ref.ProjectID != projectID {
        return fault.New("RUN_NOT_FOUND", "run_not_found", nil)
    }
    if err != nil {
        return stateError(err)
    }
    var oldData []byte
    err = tx.QueryRowContext(ctx, "SELECT data FROM run_metadata WHERE run_id=?", id).Scan(&oldData)
    if err == nil {
        var old task.RunMetadata
        if err = json.Unmarshal(oldData, &old); err != nil {
            return stateError(err)
        }
        if old.Name != next.Name || !slices.Equal(old.Tags, next.Tags) {
            return fault.New("BAD_CONFIG", "run_metadata_immutable", nil)
        }
        return nil
    }
    if !errors.Is(err, sql.ErrNoRows) {
        return stateError(err)
    }
    data, err := json.Marshal(next)
    if err != nil {
        return err
    }
    _, err = tx.ExecContext(ctx, "INSERT INTO run_metadata(run_id,project_id,data) VALUES(?,?,?)", id, projectID, data)
    if err == nil {
        err = tx.Commit()
    }
    return stateError(err)
}

// RunObservation returns dated public facts and the last query failure.
func (s *Store) RunObservation(projectID, id string) (task.RunObservation, error) {
    var out task.RunObservation
    err := s.readAux("observations", projectID, id, &out)
    return out, err
}

// SaveRunObservation merges independent success/failure observations atomically.
func (s *Store) SaveRunObservation(ctx context.Context, projectID, id string, next task.RunObservation) error {
    database, err := db.Open(ctx, s.Path, true)
    if err != nil {
        return stateError(err)
    }
    defer database.Close()
    tx, err := database.BeginTx(ctx, nil)
    if err != nil {
        return stateError(err)
    }
    defer tx.Rollback()
    ref, err := loadRun(ctx, tx, id)
    if errors.Is(err, sql.ErrNoRows) || err == nil && ref.ProjectID != projectID {
        return fault.New("RUN_NOT_FOUND", "run_not_found", nil)
    }
    if err != nil {
        return stateError(err)
    }
    var data []byte
    var old task.RunObservation
    err = tx.QueryRowContext(ctx, "SELECT data FROM observations WHERE run_id=?", id).Scan(&data)
    if err == nil {
        err = json.Unmarshal(data, &old)
    }
    if err != nil && !errors.Is(err, sql.ErrNoRows) {
        return stateError(err)
    }
    if next.Last != nil && (old.Last == nil || next.Last.ObservedAt.After(old.Last.ObservedAt)) {
        if old.Last == nil || !wire.Terminal(old.Last.Status) || sameOutcome(old.Last, next.Last) {
            old.Last = next.Last
        }
    }
    if next.Failure != nil && (old.Failure == nil || next.Failure.ObservedAt.After(old.Failure.ObservedAt)) {
        old.Failure = next.Failure
    }
    data, err = json.Marshal(old)
    if err != nil {
        return err
    }
    _, err = tx.ExecContext(ctx, "INSERT INTO observations(run_id,project_id,data) VALUES(?,?,?) ON CONFLICT(run_id) DO UPDATE SET data=excluded.data", id, projectID, data)
    if err == nil {
        err = tx.Commit()
    }
    return stateError(err)
}

// A later poll may refresh a terminal observation's date, but cannot replace
// the terminal result with contradictory execution facts.
func sameOutcome(a, b *task.ObservedRun) bool {
    left, right := *a, *b
    left.ObservedAt, right.ObservedAt = time.Time{}, time.Time{}
    return reflect.DeepEqual(left, right)
}
