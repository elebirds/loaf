// Package db owns the local SQLite schema and connection policy.
package db

import (
    "context"
    "database/sql"
    "errors"
    "fmt"
    "net/url"
    "os"
    "path/filepath"
    "time"

    "github.com/elebirds/loaf/internal/filelock"
    "github.com/elebirds/loaf/internal/localpath"
    "github.com/elebirds/loaf/internal/privatefs"
    "github.com/elebirds/loaf/internal/state/eventlog"
    "github.com/elebirds/loaf/internal/wire"
    "modernc.org/sqlite"
)

const applicationID = 0x4c4f4146
const SchemaVersion = 4

const localSchema = `
CREATE TABLE compatibility (singleton INTEGER PRIMARY KEY CHECK(singleton=1), series TEXT NOT NULL);
CREATE TABLE projects (root TEXT PRIMARY KEY, id TEXT NOT NULL UNIQUE, target TEXT NOT NULL);
CREATE TABLE runs (id TEXT PRIMARY KEY, project_id TEXT NOT NULL, attempted_at TEXT, submission TEXT NOT NULL, data BLOB NOT NULL);
CREATE INDEX runs_project ON runs(project_id, attempted_at DESC, id);
CREATE TABLE run_metadata (run_id TEXT PRIMARY KEY REFERENCES runs(id), project_id TEXT NOT NULL, data BLOB NOT NULL);
CREATE TABLE observations (run_id TEXT PRIMARY KEY REFERENCES runs(id), project_id TEXT NOT NULL, data BLOB NOT NULL);
CREATE TABLE cloud_requests (id TEXT PRIMARY KEY, created_at TEXT NOT NULL, data BLOB NOT NULL);
CREATE TABLE queue_bindings (id TEXT PRIMARY KEY, project_id TEXT NOT NULL, data BLOB NOT NULL);
CREATE TABLE notify_destinations (name TEXT PRIMARY KEY, data BLOB NOT NULL);
CREATE TABLE notify_subscriptions (id TEXT PRIMARY KEY, registration_key TEXT UNIQUE, status TEXT NOT NULL, data BLOB NOT NULL);
CREATE TABLE notify_sources (id TEXT PRIMARY KEY, subscription_id TEXT NOT NULL REFERENCES notify_subscriptions(id), next_poll_at TEXT NOT NULL, data BLOB NOT NULL);
CREATE INDEX notify_source_due ON notify_sources(next_poll_at,id);
CREATE TABLE notify_cursors (binding_id TEXT PRIMARY KEY REFERENCES notify_sources(id), source_id TEXT NOT NULL, cursor TEXT NOT NULL);
CREATE TABLE notify_inbox (received_sequence INTEGER PRIMARY KEY AUTOINCREMENT, event_id TEXT NOT NULL UNIQUE, source_id TEXT NOT NULL, sequence INTEGER NOT NULL, payload BLOB NOT NULL, UNIQUE(source_id,sequence));
CREATE TABLE notify_deliveries (id TEXT PRIMARY KEY, subscription_id TEXT NOT NULL REFERENCES notify_subscriptions(id), destination TEXT NOT NULL, event_id TEXT NOT NULL, stage TEXT NOT NULL, next_attempt_at TEXT NOT NULL, next_lookup_at TEXT NOT NULL, parent_id TEXT UNIQUE, data BLOB NOT NULL);
CREATE INDEX notify_pending_due ON notify_deliveries(stage,next_attempt_at,id);
CREATE INDEX notify_lookup_due ON notify_deliveries(stage,next_lookup_at,id);
CREATE TABLE notify_dedup (subscription_id TEXT NOT NULL, destination TEXT NOT NULL, event_id TEXT NOT NULL, delivery_id TEXT NOT NULL, PRIMARY KEY(subscription_id,destination,event_id));
CREATE TABLE notify_attempts (delivery_id TEXT NOT NULL REFERENCES notify_deliveries(id) ON DELETE CASCADE, number INTEGER NOT NULL, data BLOB NOT NULL, PRIMARY KEY(delivery_id,number));
` + eventlog.Schema + `
PRAGMA application_id=1280262470;
PRAGMA user_version=4;
`

// Open opens an existing database read-only, or initializes the current schema
// for a writer. Readers never initialize directories, tables, or state.
func Open(ctx context.Context, path string, write bool) (*sql.DB, error) {
    if !filepath.IsAbs(path) {
        return nil, errors.New("state path must be absolute")
    }
    if write {
        if err := privatefs.MkdirAll(filepath.Dir(path)); err != nil {
            return nil, err
        }
    }
    if err := privatefs.Check(filepath.Dir(path), true); err != nil {
        return nil, err
    }
    created := false
    info, err := os.Lstat(path)
    if os.IsNotExist(err) && write {
        file, createErr := privatefs.Create(path)
        if createErr == nil {
            created = true
            createErr = file.Close()
        }
        if createErr != nil && !os.IsExist(createErr) {
            return nil, createErr
        }
        info, err = os.Lstat(path)
    }
    if err != nil {
        return nil, err
    }
    if !info.Mode().IsRegular() {
        return nil, errors.New("unsafe state database")
    }
    for _, name := range []string{filepath.Dir(path), path, path + "-wal", path + "-shm"} {
        err := privatefs.Check(name, name == filepath.Dir(path))
        if os.IsNotExist(err) && (name == path+"-wal" || name == path+"-shm") {
            continue
        }
        if err != nil {
            return nil, err
        }
    }
    uri := localpath.FileURL(path)
    q := url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(2000)", "foreign_keys(1)"}, "_txlock": {"immediate"}}
    if write {
        q.Set("mode", "rw")
    }
    uri.RawQuery = q.Encode()
    database, err := sql.Open("sqlite", uri.String())
    if err != nil {
        return nil, err
    }
    database.SetMaxOpenConns(1)
    fail := func(err error) (*sql.DB, error) {
        database.Close()
        return nil, err
    }
    var version, app int
    readSchema := func() error {
        // One statement observes both identifiers in the same SQLite snapshot,
        // including while another process is committing initial schema setup.
        return database.QueryRowContext(ctx, "SELECT user_version, application_id FROM pragma_user_version, pragma_application_id").Scan(&version, &app)
    }
    if err = readSchema(); err != nil {
        return fail(err)
    }
    initializationLocked := false
    if write && version == 0 && app == 0 {
        unlock, err := lockInitialization(ctx, path+".init.lock")
        if err != nil {
            return fail(err)
        }
        defer unlock()
        initializationLocked = true
        // Another opener may have initialized the file while we waited. Only
        // schema/WAL setup needs this lock; ordinary writers use SQLite locks.
        if err = readSchema(); err != nil {
            return fail(err)
        }
    }
    if write && version == 0 && app == 0 {
        tx, err := database.BeginTx(ctx, nil)
        if err != nil {
            return fail(err)
        }
        var tables int
        err = tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version)
        if err == nil && version == 0 {
            err = tx.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master").Scan(&tables)
            if err == nil && tables != 0 {
                err = errors.New("unknown state schema")
            }
            if err == nil {
                _, err = tx.ExecContext(ctx, localSchema)
            }
            if err == nil {
                _, err = tx.ExecContext(ctx, "INSERT INTO compatibility VALUES(1,?)", wire.StateSeries(wire.Version))
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
        if err = readSchema(); err != nil {
            return fail(err)
        }
    }
    if version != SchemaVersion || app != applicationID {
        return fail(fmt.Errorf("unsupported state schema: %d", version))
    }
    var series string
    if err = database.QueryRowContext(ctx, "SELECT series FROM compatibility WHERE singleton=1").Scan(&series); err != nil {
        return fail(errors.New("invalid state compatibility series"))
    }
    if series != wire.StateSeries(wire.Version) {
        return fail(errors.New("unsupported state compatibility series: " + series))
    }
    if write {
        var journal string
        if err = database.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil {
            return fail(err)
        }
        if journal != "wal" {
            if !initializationLocked {
                unlock, err := lockInitialization(ctx, path+".init.lock")
                if err != nil {
                    return fail(err)
                }
                defer unlock()
            }
            if err = database.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&journal); err != nil {
                return fail(err)
            }
            if journal != "wal" {
                return fail(errors.New("state requires WAL journaling"))
            }
        }
        if _, err = database.ExecContext(ctx, "PRAGMA synchronous=FULL"); err != nil {
            return fail(err)
        }
        conn, err := database.Conn(ctx)
        if err != nil {
            return fail(err)
        }
        err = conn.Raw(func(raw any) error {
            _, err := raw.(sqlite.FileControl).FileControlPersistWAL("main", 1)
            return err
        })
        _ = conn.Close()
        if err != nil {
            return fail(err)
        }
    }
    if !write {
        if _, err = database.ExecContext(ctx, "PRAGMA query_only=ON"); err != nil {
            return fail(err)
        }
    }
    if created {
        if err = privatefs.SyncParent(path); err != nil {
            return fail(err)
        }
    }
    return database, nil
}

// lockInitialization serializes only connection/schema setup across processes.
// The caller releases it before returning a connection or starting any use-case transaction.
func lockInitialization(ctx context.Context, path string) (func(), error) {
    file, err := filelock.Acquire(ctx, path, 2*time.Second)
    if err != nil {
        return nil, err
    }
    return func() { _ = file.Close() }, nil
}
