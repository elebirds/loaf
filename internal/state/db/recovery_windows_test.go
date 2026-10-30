package db

import (
    "bufio"
    "context"
    "fmt"
    "os"
    "os/exec"
    "path/filepath"
    "testing"

    "github.com/elebirds/loaf/internal/privatefs"
)

func TestWindowsDatabaseCrashHelper(t *testing.T) {
    path := os.Getenv("LOAF_DATABASE_CRASH_HELPER")
    if path == "" {
        return
    }
    database, err := Open(context.Background(), path, true)
    if err != nil {
        t.Fatal(err)
    }
    if _, err := database.Exec("INSERT INTO projects VALUES('committed','p_committed','')"); err != nil {
        t.Fatal(err)
    }
    tx, err := database.Begin()
    if err != nil {
        t.Fatal(err)
    }
    if _, err := tx.Exec("INSERT INTO projects VALUES('uncommitted','p_uncommitted','')"); err != nil {
        t.Fatal(err)
    }
    fmt.Println("ready")
    var input [1]byte
    os.Stdin.Read(input[:])
    os.Exit(90) // Parent must kill this process without a clean SQL close.
}

func TestWindowsDatabaseRecoversCommittedWALAfterProcessDeath(t *testing.T) {
    path := filepath.Join(t.TempDir(), "private 中文", "state.db")
    child := exec.Command(os.Args[0], "-test.run=^TestWindowsDatabaseCrashHelper$")
    child.Env = append(os.Environ(), "LOAF_DATABASE_CRASH_HELPER="+path)
    input, err := child.StdinPipe()
    if err != nil {
        t.Fatal(err)
    }
    defer input.Close()
    output, err := child.StdoutPipe()
    if err != nil {
        t.Fatal(err)
    }
    defer output.Close()
    if err := child.Start(); err != nil {
        t.Fatal(err)
    }
    defer func() { child.Process.Kill(); child.Wait() }()
    if line, err := bufio.NewReader(output).ReadString('\n'); err != nil || line != "ready\n" {
        t.Fatal(line, err)
    }
    for _, file := range []string{path, path + "-wal", path + "-shm"} {
        if err := privatefs.Check(file, false); err != nil {
            t.Fatal("unsafe SQLite file", file, err)
        }
    }
    if err := child.Process.Kill(); err != nil {
        t.Fatal(err)
    }
    child.Wait()
    database, err := Open(context.Background(), path, false)
    if err != nil {
        t.Fatal(err)
    }
    defer database.Close()
    var count int
    if err := database.QueryRow("SELECT count(*) FROM projects WHERE root='committed'").Scan(&count); err != nil || count != 1 {
        t.Fatal(count, err)
    }
    if err := database.QueryRow("SELECT count(*) FROM projects WHERE root='uncommitted'").Scan(&count); err != nil || count != 0 {
        t.Fatal(count, err)
    }
}
