package remote

import (
    "errors"
    "os"
    "path/filepath"
    "time"

    remotestate "github.com/elebirds/loaf/internal/state/remote"
    "github.com/elebirds/loaf/internal/wire"
    "golang.org/x/sys/unix"
)

type stopRequest struct {
    FormatVersion int   `json:"format_version"`
    GraceMillis   int64 `json:"grace_millis"`
}

// lockRun serializes stop acceptance, phase admission, and final publication.
// It is separate from owner.lock, which the supervisor holds for its lifetime.
func lockRun(dir string) (*os.File, error) {
    f, err := os.OpenFile(filepath.Join(dir, "control.lock"), os.O_CREATE|os.O_RDWR|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
    if err != nil {
        if errors.Is(err, os.ErrNotExist) {
            return nil, errors.New("RUN_NOT_FOUND")
        }
        return nil, errors.New("WRITE_FAILED")
    }
    info, err := f.Stat()
    if err != nil || !info.Mode().IsRegular() {
        f.Close()
        return nil, errors.New("BAD_FORMAT")
    }
    for {
        err = unix.Flock(int(f.Fd()), unix.LOCK_EX)
        if !errors.Is(err, unix.EINTR) {
            break
        }
    }
    if err != nil {
        f.Close()
        return nil, errors.New("WRITE_FAILED")
    }
    return f, nil
}

func readStop(dir string) (*stopRequest, error) {
    state, err := remotestate.Open(filepath.Dir(filepath.Dir(dir)), false)
    if err != nil {
        return nil, err
    }
    defer state.Close()
    grace, err := state.Stop(filepath.Base(dir))
    if err != nil || grace == nil {
        return nil, err
    }
    return &stopRequest{wire.FormatVersion, *grace}, nil
}

func stop(req wire.Request, dir string) (*wire.RunRecord, error) {
    grace := wire.DefaultStopGrace
    if req.GraceMillis != nil {
        if *req.GraceMillis < 0 || *req.GraceMillis > wire.MaxStopGrace.Milliseconds() {
            return nil, errors.New("BAD_REQUEST")
        }
        grace = time.Duration(*req.GraceMillis) * time.Millisecond
    }
    if _, err := read(dir); err != nil {
        return nil, err
    }
    lock, err := lockRun(dir)
    if err != nil {
        return nil, err
    }
    defer lock.Close()
    r, err := status(dir)
    if err != nil || terminal(r.Status) {
        return r, err
    }
    if r.Status == "unknown" || r.SupervisorPID == 0 {
        return r, errors.New("STOP_UNCONFIRMED")
    }
    existing, err := readStop(dir)
    if err != nil {
        return r, err
    }
    if existing == nil {
        existing = &stopRequest{1, grace.Milliseconds()}
        state, err := remotestate.Open(req.Root, true)
        if err != nil {
            return r, err
        }
        err = state.AcceptStop(req.RunID, existing.GraceMillis)
        state.Close()
        if err != nil {
            return r, err
        }
    }
    r.StopGraceMillis = &existing.GraceMillis
    return r, nil
}
