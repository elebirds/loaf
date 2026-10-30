package db

import (
    "context"
    "errors"
    "path/filepath"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/filelock"
    "github.com/elebirds/loaf/internal/privatefs"
)

func TestInitializedDatabaseDoesNotSerializeConnectionSetup(t *testing.T) {
    path := filepath.Join(t.TempDir(), "private", "state.db")
    database, err := Open(context.Background(), path, true)
    if err != nil {
        t.Fatal(err)
    }
    database.Close()
    lock, err := filelock.Acquire(context.Background(), path+".init.lock", 0)
    if err != nil {
        t.Fatal(err)
    }
    defer lock.Close()
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    database, err = Open(ctx, path, true)
    if err != nil {
        t.Fatal("initialized connection waited for schema setup", err)
    }
    defer database.Close()
    if _, err := database.ExecContext(ctx, "INSERT INTO projects VALUES('root','p_test','')"); err != nil {
        t.Fatal(err)
    }
}

func TestUninitializedDatabaseWaitCanBeCanceled(t *testing.T) {
    path := filepath.Join(t.TempDir(), "private", "state.db")
    if err := privatefs.MkdirAll(filepath.Dir(path)); err != nil {
        t.Fatal(err)
    }
    file, err := privatefs.Create(path)
    if err != nil {
        t.Fatal(err)
    }
    file.Close()
    lock, err := filelock.Acquire(context.Background(), path+".init.lock", 0)
    if err != nil {
        t.Fatal(err)
    }
    ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
    defer cancel()
    _, err = Open(ctx, path, true)
    lock.Close()
    if !errors.Is(err, context.DeadlineExceeded) {
        t.Fatal("schema setup ignored cancellation", err)
    }
    database, err := Open(context.Background(), path, true)
    if err != nil {
        t.Fatal("canceled initialization did not recover", err)
    }
    database.Close()
}
