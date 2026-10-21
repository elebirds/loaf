package remote

import (
    "bytes"
    "encoding/json"
    "errors"
    "io"
    "os"
    "path/filepath"
    "time"

    "github.com/elebirds/loaf/internal/atomicfile"
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
    f, err := os.OpenFile(filepath.Join(dir, "stop.request"), os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
    if errors.Is(err, os.ErrNotExist) {
        return nil, nil
    }
    if err != nil {
        return nil, errors.New("BAD_FORMAT")
    }
    defer f.Close()
    info, err := f.Stat()
    if err != nil || !info.Mode().IsRegular() {
        return nil, errors.New("BAD_FORMAT")
    }
    data, err := io.ReadAll(io.LimitReader(f, 1025))
    if err != nil || len(data) > 1024 {
        return nil, errors.New("BAD_FORMAT")
    }
    // Retain the original internal stop marker's meaning.
    if bytes.Equal(data, []byte("stop\n")) {
        return &stopRequest{1, wire.DefaultStopGrace.Milliseconds()}, nil
    }
    var req stopRequest
    decoder := json.NewDecoder(bytes.NewReader(data))
    decoder.DisallowUnknownFields()
    if decoder.Decode(&req) != nil || decoder.Decode(new(any)) != io.EOF || req.FormatVersion != 1 || req.GraceMillis < 0 || req.GraceMillis > wire.MaxStopGrace.Milliseconds() {
        return nil, errors.New("BAD_FORMAT")
    }
    return &req, nil
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
        data, err := json.Marshal(existing)
        if err != nil || atomicfile.Write(filepath.Join(dir, "stop.request"), append(data, '\n')) != nil {
            return r, errors.New("WRITE_FAILED")
        }
    }
    r.StopGraceMillis = &existing.GraceMillis
    return r, nil
}
