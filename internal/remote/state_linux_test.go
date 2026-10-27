package remote

import (
    "context"
    "database/sql"
    "encoding/json"
    "net/url"
    "path/filepath"
    "strings"
    "testing"

    remotestate "github.com/elebirds/loaf/internal/state/remote"
    "github.com/elebirds/loaf/internal/wire"
    "modernc.org/sqlite"
)

func fixtureSQL(root, query string, args ...any) error {
    db, err := sql.Open("sqlite", remotestate.Path(root))
    if err != nil {
        return err
    }
    defer db.Close()
    conn, err := db.Conn(context.Background())
    if err != nil {
        return err
    }
    err = conn.Raw(func(raw any) error { _, err := raw.(sqlite.FileControl).FileControlPersistWAL("main", 1); return err })
    conn.Close()
    if err != nil {
        return err
    }
    _, err = db.Exec(query, args...)
    return err
}

func testSQL(t *testing.T, root, query string, args ...any) {
    t.Helper()
    if err := fixtureSQL(root, query, args...); err != nil {
        t.Fatal(err)
    }
}

// seedRecord deliberately bypasses lifecycle guards to construct adversarial
// fixtures. Production writers use CreateRun or SaveRun, never this helper.
func seedRecord(dir string, r *wire.RunRecord) error {
    state, err := remotestate.Open(filepath.Dir(filepath.Dir(dir)), true)
    if err != nil {
        return err
    }
    defer state.Close()
    if err := state.CreateRun(r); err == nil {
        return nil
    } else if err.Error() != "RUN_EXISTS" {
        return err
    }
    if err := fixtureSQL(filepath.Dir(filepath.Dir(dir)), "UPDATE runs SET status='fixture' WHERE run_id=?", r.Request.RunID); err != nil {
        return err
    }
    return state.SaveRun(r)
}

func recordBytes(dir string) ([]byte, error) {
    db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: remotestate.Path(filepath.Dir(filepath.Dir(dir))), RawQuery: "mode=ro"}).String())
    if err != nil {
        return nil, err
    }
    defer db.Close()
    var raw string
    err = db.QueryRow(`SELECT json_array(format_version,request,status,phase,created_at,completed_at,supervisor_pid,supervisor_start,boot_id,child_pid,exit_code,signal,reason,copy_millis,stop_grace_millis) FROM runs WHERE run_id=?`, filepath.Base(dir)).Scan(&raw)
    return []byte(raw), err
}

func stopBytes(dir string) ([]byte, error) {
    stop, err := readStop(dir)
    if err != nil {
        return nil, err
    }
    if stop == nil {
        return nil, nil
    }
    return json.Marshal(stop)
}

func testUnknownRecord(t *testing.T, root, id, text string) {
    t.Helper()
    if strings.Contains(text, `"format_version":99`) {
        testSQL(t, root, "UPDATE runs SET format_version=99 WHERE run_id=?", id)
    } else {
        var record wire.RunRecord
        if err := json.Unmarshal([]byte(text), &record); err != nil {
            t.Fatal(err)
        }
        request, _ := json.Marshal(record.Request)
        testSQL(t, root, "UPDATE runs SET format_version=?, request=? WHERE run_id=?", record.FormatVersion, string(request), id)
    }
}
