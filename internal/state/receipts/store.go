// Package receipts persists cloud request documents in the local control database.
package receipts

import (
    "context"
    "database/sql"
    "os"

    "github.com/elebirds/loaf/internal/event"
    "github.com/elebirds/loaf/internal/state/db"
    "github.com/elebirds/loaf/internal/state/eventlog"
)

// Store shares the local state database without depending on cloud policy types.
type Store struct {
    Path string
}

// SaveEvent atomically saves the request and its public confirmed transition.
func (s Store) SaveEvent(ctx context.Context, id, created string, data []byte, key string, fact *event.Envelope) error {

    database, err := db.Open(ctx, s.Path, true)
    if err != nil {
        return err
    }
    defer database.Close()
    tx, err := database.BeginTx(ctx, nil)
    if err != nil {
        return err
    }
    defer tx.Rollback()
    _, err = tx.ExecContext(ctx, `INSERT INTO cloud_requests(id,created_at,data) VALUES(?,?,?)
        ON CONFLICT(id) DO UPDATE SET data=excluded.data`, id, created, data)
    if err != nil {
        return err
    }
    if fact != nil {
        if _, err = eventlog.Append(ctx, tx, key, *fact); err != nil {
            return err
        }
    }
    return tx.Commit()

}

// Load reads one original request without creating storage.
func (s Store) Load(ctx context.Context, id string) ([]byte, error) {

    database, err := db.Open(ctx, s.Path, false)
    if err != nil {
        return nil, err
    }
    defer database.Close()
    var data []byte
    err = database.QueryRowContext(ctx, "SELECT data FROM cloud_requests WHERE id=?", id).Scan(&data)
    if err == sql.ErrNoRows {
        err = os.ErrNotExist
    }
    return data, err

}

// List returns the bounded durable request inventory in deterministic order.
func (s Store) List(ctx context.Context) ([][]byte, error) {

    database, err := db.Open(ctx, s.Path, false)
    if os.IsNotExist(err) {
        return [][]byte{}, nil
    }
    if err != nil {
        return nil, err
    }
    defer database.Close()
    rows, err := database.QueryContext(ctx, "SELECT data FROM cloud_requests ORDER BY created_at DESC,id LIMIT 10001")
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    result := [][]byte{}
    for rows.Next() {

        var data []byte
        if err := rows.Scan(&data); err != nil {
            return nil, err
        }
        result = append(result, data)

    }
    return result, rows.Err()

}
