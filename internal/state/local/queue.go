package local

import (
    "context"
    "database/sql"
    "errors"

    "github.com/elebirds/loaf/internal/state/db"
)

// SaveQueueBinding persists an immutable private request locator before upload.
// Queue payloads contain no execution environment; only frozen navigation facts.
func (s *Store) SaveQueueBinding(ctx context.Context, id, project string, data []byte) error {
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
    var existing []byte
    err = tx.QueryRowContext(ctx, "SELECT data FROM queue_bindings WHERE id=? AND project_id=?", id, project).Scan(&existing)
    if err == nil {
        if string(existing) != string(data) {
            return stateError(errors.New("QUEUE_BINDING_EXISTS"))
        }
        return nil
    }
    if !errors.Is(err, sql.ErrNoRows) {
        return stateError(err)
    }
    if _, err = tx.ExecContext(ctx, "INSERT INTO queue_bindings(id,project_id,data) VALUES(?,?,?)", id, project, data); err != nil {
        return stateError(err)
    }
    return stateError(tx.Commit())
}

// QueueBinding reads an exact project-scoped locator without current aliases.
func (s *Store) QueueBinding(ctx context.Context, id, project string) ([]byte, error) {
    database, err := db.Open(ctx, s.Path, false)
    if err != nil {
        return nil, stateError(err)
    }
    defer database.Close()
    var data []byte
    err = database.QueryRowContext(ctx, "SELECT data FROM queue_bindings WHERE id=? AND project_id=?", id, project).Scan(&data)
    return data, stateError(err)
}
