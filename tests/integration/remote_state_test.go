package integration

import (
    "database/sql"
    "strings"

    remotestate "github.com/elebirds/loaf/internal/state/remote"
)

// managedRemote installs only a fresh control database for low-level runner
// fixtures. End-to-end CLI tests initialize through the real receiver instead.
func (f *fixture) managedRemote(root string) {
    f.t.Helper()
    local := f.t.TempDir()
    if err := remotestate.Initialize(local, "project"); err != nil {
        f.t.Fatal(err)
    }
    database, err := sql.Open("sqlite", remotestate.Path(local))
    if err != nil {
        f.t.Fatal(err)
    }
    if _, err := database.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
        f.t.Fatal(err)
    }
    database.Close()
    f.shell(`mkdir -m 700 -p "$1/.loaf-control-v1" "$1/cache"`, root)
    command(f.t, "docker", "cp", remotestate.Path(local), f.id+":"+remotestate.Path(root))
    command(f.t, "docker", "exec", f.id, "chown", "researcher:researcher", remotestate.Path(root))
    command(f.t, "docker", "exec", f.id, "chmod", "0600", remotestate.Path(root))
}

func (f *fixture) remoteSQL(root, query string) string {
    f.t.Helper()
    return f.shell(`sqlite3 -readonly "$1/.loaf-control-v1/state.sqlite3" "$2"`, root, query)
}

func (f *fixture) remoteRecord(root, id string) string {
    f.t.Helper()
    query := `SELECT json_object('format_version',format_version,'request',json(request),'status',status,'phase',phase,'created_at',created_at,'completed_at',completed_at,'supervisor_pid',supervisor_pid,'supervisor_start',supervisor_start,'boot_id',boot_id,'child_pid',child_pid,'exit_code',exit_code,'signal',signal,'reason',reason,'copy_millis',copy_millis,'stop_grace_millis',stop_grace_millis) FROM runs WHERE run_id='` + strings.ReplaceAll(id, "'", "''") + `'`
    return f.remoteSQL(root, query)
}
