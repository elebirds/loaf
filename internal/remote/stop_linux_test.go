package remote

import (
    "bytes"
    "os"
    "path/filepath"
    "strings"
    "sync"
    "testing"
    "time"

    "github.com/elebirds/loaf/internal/wire"
    "golang.org/x/sys/unix"
)

func ownedRun(t *testing.T) (wire.Request, string) {
    t.Helper()
    root := managedRoot(t)
    dir := filepath.Join(root, "runs", "owned")
    if err := os.MkdirAll(filepath.Join(dir, "work"), 0700); err != nil {
        t.Fatal(err)
    }
    owner, err := os.OpenFile(filepath.Join(dir, "owner.lock"), os.O_CREATE|os.O_RDWR, 0600)
    if err != nil {
        t.Fatal(err)
    }
    t.Cleanup(func() { owner.Close() })
    if err := unix.Flock(int(owner.Fd()), unix.LOCK_EX); err != nil {
        t.Fatal(err)
    }
    identity, err := process(os.Getpid())
    if err != nil {
        t.Fatal(err)
    }
    boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
    if err != nil {
        t.Fatal(err)
    }
    req := wire.Request{FormatVersion: 1, Version: wire.Version, Action: "stop", Root: root, ProjectID: "project", RunID: "owned"}
    r := &wire.RunRecord{FormatVersion: 1, Request: req, Status: "running", Phase: "setup", CreatedAt: time.Now(), SupervisorPID: os.Getpid(), SupervisorStart: identity.start, BootID: strings.TrimSpace(string(boot))}
    if err := seedRecord(dir, r); err != nil {
        t.Fatal(err)
    }
    return req, dir
}

func TestStopKeepsFirstGraceAndRunRecord(t *testing.T) {
    req, dir := ownedRun(t)
    before, _ := recordBytes(dir)
    first := int64(750)
    req.GraceMillis = &first
    r, err := Handle(req)
    if err != nil || r.Status != "running" || r.StopGraceMillis == nil || *r.StopGraceMillis != first {
        t.Fatal(r, err)
    }
    accepted, err := stopBytes(dir)
    if err != nil {
        t.Fatal(err)
    }
    zero := int64(0)
    req.GraceMillis = &zero
    var wg sync.WaitGroup
    for i := 0; i < 8; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            r, err := Handle(req)
            if err != nil || r.StopGraceMillis == nil || *r.StopGraceMillis != first {
                t.Errorf("duplicate stop: %+v %v", r, err)
            }
        }()
    }
    wg.Wait()
    after, _ := stopBytes(dir)
    record, _ := recordBytes(dir)
    if !bytes.Equal(accepted, after) || !bytes.Equal(before, record) {
        t.Fatal("stop rewrote its first policy or the execution owner's record")
    }
}

func TestAcceptedStopPreventsPhaseAdmission(t *testing.T) {
    for _, phase := range []string{"setup", "command"} {
        t.Run(phase, func(t *testing.T) {
            req, dir := ownedRun(t)
            if _, err := Handle(req); err != nil {
                t.Fatal(err)
            }
            r, err := read(dir)
            if err != nil {
                t.Fatal(err)
            }
            log, err := os.Create(filepath.Join(dir, "output.log"))
            if err != nil {
                t.Fatal(err)
            }
            defer log.Close()
            if err := executePhase(dir, r, phase, []string{"/bin/sh", "-c", "touch admitted"}, log); err != nil {
                t.Fatal(err)
            }
            if r.Status != "stopped" || r.Reason != "STOP_BEFORE_START" || r.ChildPID != 0 || r.ExitCode != nil {
                t.Fatalf("admitted after stop: %+v", r)
            }
            if _, err := os.Stat(filepath.Join(dir, "work", "admitted")); !os.IsNotExist(err) {
                t.Fatal("phase command executed")
            }
        })
    }
}

func TestStopPreservesTerminalAndUnknownResults(t *testing.T) {
    for _, state := range []string{"succeeded", "failed", "stopped", "unknown"} {
        t.Run(state, func(t *testing.T) {
            req, dir := ownedRun(t)
            r, _ := read(dir)
            r.Status = state
            r.Reason = "retained"
            if state == "succeeded" || state == "failed" {
                code := 7
                if state == "succeeded" {
                    code = 0
                }
                r.ExitCode = &code
            }
            if err := seedRecord(dir, r); err != nil {
                t.Fatal(err)
            }
            before, _ := recordBytes(dir)
            got, err := Handle(req)
            if state == "unknown" {
                if err == nil || err.Error() != "STOP_UNCONFIRMED" {
                    t.Fatal(got, err)
                }
            } else if err != nil {
                t.Fatal(err)
            }
            after, _ := recordBytes(dir)
            if got.Status != state || got.Reason != "retained" || !bytes.Equal(before, after) {
                t.Fatal("retained result changed", got)
            }
            if request, err := readStop(dir); err != nil || request != nil {
                t.Fatal("unnecessary or blind stop request")
            }
        })
    }
}

func TestStopPolicyValidation(t *testing.T) {
    for _, value := range []int64{-1, wire.MaxStopGrace.Milliseconds() + 1} {
        req, _ := ownedRun(t)
        req.GraceMillis = &value
        if _, err := Handle(req); err == nil || err.Error() != "BAD_REQUEST" {
            t.Fatal("accepted invalid grace", value, err)
        }
    }
    req, dir := ownedRun(t)
    testSQL(t, req.Root, "PRAGMA ignore_check_constraints=ON; INSERT INTO stop_requests VALUES('owned', -1)")
    if _, err := readStop(dir); err == nil {
        t.Fatal("accepted invalid stored stop policy")
    }

}

func TestStopRefusesInvalidSupervisorIdentities(t *testing.T) {
    for _, kind := range []string{"start", "boot", "owner", "not_yet_owned"} {
        t.Run(kind, func(t *testing.T) {
            req, dir := ownedRun(t)
            r, err := read(dir)
            if err != nil {
                t.Fatal(err)
            }
            switch kind {
            case "start":
                r.SupervisorStart = "different"
            case "boot":
                r.BootID = "different"
            case "owner":
                if err := os.Remove(filepath.Join(dir, "owner.lock")); err != nil {
                    t.Fatal(err)
                }
            case "not_yet_owned":
                r.SupervisorPID = 0
            }
            if err := seedRecord(dir, r); err != nil {
                t.Fatal(err)
            }
            before, _ := recordBytes(dir)
            r, err = Handle(req)
            if err == nil || err.Error() != "STOP_UNCONFIRMED" || r.ExitCode != nil {
                t.Fatal(r, err)
            }
            after, _ := recordBytes(dir)
            if !bytes.Equal(before, after) {
                t.Fatal("stop rewrote an unowned run")
            }
            if request, err := readStop(dir); err != nil || request != nil {
                t.Fatal("stop accepted without a valid owner")
            }
        })
    }
}
