package remote

import (
    "bytes"
    "encoding/json"
    "errors"
    "os"
    "path/filepath"
    "testing"

    "github.com/elebirds/loaf/internal/wire"
    "golang.org/x/sys/unix"
)

func TestDuplicateStartBeforePublication(t *testing.T) {
    for _, tc := range []struct {
        name, claimID, wantError string
        locked, directory, done  bool
    }{
        {name: "before_directory", claimID: "duplicate", locked: true, wantError: "SUBMISSION_UNKNOWN"},
        {name: "before_record", claimID: "duplicate", locked: true, directory: true, wantError: "SUBMISSION_UNKNOWN"},
        {name: "retained_result", claimID: "duplicate", locked: true, directory: true, done: true},
        {name: "another_run", claimID: "other", locked: true, wantError: "PROJECT_BUSY"},
        {name: "unlocked_claim_file", claimID: "duplicate", wantError: "PROJECT_BUSY"},
    } {
        t.Run(tc.name, func(t *testing.T) {
            root := managedRoot(t)
            if err := os.MkdirAll(filepath.Join(root, "runs"), 0700); err != nil {
                t.Fatal(err)
            }
            req := wire.Request{FormatVersion: wire.FormatVersion, Version: wire.Version, Action: "start", Root: root, ProjectID: "project", RunID: "duplicate", Args: []string{"/bin/true"}}
            dir := filepath.Join(root, "runs", req.RunID)
            if tc.directory {
                if err := os.Mkdir(dir, 0700); err != nil {
                    t.Fatal(err)
                }
            }
            var original []byte
            recordPath := filepath.Join(dir, "run.json")
            if tc.done {
                code := 7
                if err := save(dir, &wire.RunRecord{FormatVersion: wire.FormatVersion, Request: req, Status: "failed", ExitCode: &code}); err != nil {
                    t.Fatal(err)
                }
                var err error
                original, err = os.ReadFile(recordPath)
                if err != nil {
                    t.Fatal(err)
                }
            }
            claim, err := os.OpenFile(filepath.Join(root, "runs", "."+tc.claimID+".claim"), os.O_CREATE|os.O_RDWR, 0600)
            if err != nil {
                t.Fatal(err)
            }
            t.Cleanup(func() { claim.Close() })
            if tc.locked {
                if err := unix.Flock(int(claim.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
                    t.Fatal(err)
                }
            }
            // Keep the project lock held throughout the request. A live same-ID
            // claim pins the window before its directory or record is published.
            projectLock, err := lockProject(root)
            if err != nil {
                t.Fatal(err)
            }
            t.Cleanup(func() { projectLock.Close() })
            data, err := json.Marshal(req)
            if err != nil {
                t.Fatal(err)
            }
            var out bytes.Buffer
            exit := Serve(bytes.NewReader(data), &out)
            var response wire.Response
            if err := json.Unmarshal(out.Bytes(), &response); err != nil {
                t.Fatal(out.String(), err)
            }
            if response.FormatVersion != wire.FormatVersion || response.Version != wire.Version {
                t.Fatal(response)
            }
            if tc.wantError != "" {
                if exit != 1 || response.OK || response.Error == nil || response.Error.Code != tc.wantError || response.Record != nil {
                    t.Fatalf("want %s without a run record, got exit=%d %s", tc.wantError, exit, out.String())
                }
            } else {
                r := response.Record
                if exit != 0 || !response.OK || response.Error != nil || r == nil || r.Request.RunID != req.RunID || r.Status != "failed" || r.ExitCode == nil || *r.ExitCode != 7 {
                    t.Fatalf("lost the retained result: exit=%d %s", exit, out.String())
                }
            }
            if !tc.directory {
                if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
                    t.Fatal("contended start created a run directory", err)
                }
            }
            if tc.done {
                after, err := os.ReadFile(recordPath)
                if err != nil || !bytes.Equal(after, original) {
                    t.Fatal("duplicate start changed the retained record", err)
                }
            } else if _, err := os.Lstat(recordPath); !errors.Is(err, os.ErrNotExist) {
                t.Fatal("contended start published a run record", err)
            }
        })
    }
}
