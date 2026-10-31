package remote

import (
    "bytes"
    "context"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "syscall"
    "time"
    "unicode/utf8"

    "github.com/elebirds/loaf/internal/wire"
    "golang.org/x/sys/unix"
)

const logLimit = 256 * 1024

func validateObservation(req wire.Request) error {
    if req.WaitMillis < 0 || req.WaitMillis > 300000 || req.Tail < 0 || req.Tail > 10000 {
        return errors.New("BAD_REQUEST")
    }
    return nil
}

func observationStatus(req wire.Request) (*wire.RunRecord, error) {
    req.Action = "status"
    record, err := Handle(req)
    if record != nil {
        // Observation needs identity and outcome, not executable parameters.
        record.Request.Args = nil
        record.Request.Setup = ""
        record.Request.Env = nil
    }
    return record, err
}

// Observe waits for at most the caller's budget, then returns one bounded tail.
func Observe(ctx context.Context, req wire.Request) (wire.Observation, error) {
    var result wire.Observation
    if err := validateObservation(req); err != nil {
        return result, err
    }
    deadline := time.Now().Add(time.Duration(req.WaitMillis) * time.Millisecond)
    for {
        record, err := observationStatus(req)
        if err != nil {
            return result, err
        }
        result.Record = record
        if terminal(record.Status) || record.Status == "unknown" || !time.Now().Before(deadline) {
            break
        }
        select {
        case <-ctx.Done():
            return result, ctx.Err()
        case <-time.After(100 * time.Millisecond):
        }
    }
    log, err := readLogRequest(req, req.Resume)
    result.Log = log
    return result, err
}

// Follow streams observations only. Its lifetime never controls the supervisor.
func Follow(ctx context.Context, req wire.Request, out io.Writer) error {
    if err := validateObservation(req); err != nil {
        return err
    }
    deadline := time.Time{}
    if req.WaitMillis > 0 {
        deadline = time.Now().Add(time.Duration(req.WaitMillis) * time.Millisecond)
    }
    cursor := req.Resume
    encoder := json.NewEncoder(out)
    lastPhase, lastStatus := "", ""
    for {
        record, err := observationStatus(req)
        if err != nil {
            return err
        }
        chunk, err := readLogRequest(req, cursor)
        if err != nil {
            return err
        }
        next := chunk.Offset
        changed := lastPhase != record.Phase || lastStatus != record.Status
        if cursor == "" || chunk.Text != "" || changed {
            if err := encoder.Encode(wire.LogEvent{Type: "chunk", Record: record, Log: chunk}); err != nil {
                return err
            }
        }
        lastPhase, lastStatus = record.Phase, record.Status
        cursor = chunk.Cursor
        ended := terminal(record.Status) || record.Status == "unknown" || record.SupervisorPID == 0
        expired := !deadline.IsZero() && !time.Now().Before(deadline)
        if ended || expired {
            // Terminal records are committed after log.Sync. Drain all remaining
            // bytes before emitting end, including a final chunk over the limit.
            final, err := readLogRequest(req, cursor)
            if err != nil {
                return err
            }
            if final.Text != "" {
                if err := encoder.Encode(wire.LogEvent{Type: "chunk", Record: record, Log: final}); err != nil {
                    return err
                }
                next = final.Offset
                cursor = final.Cursor
                if final.Truncated {
                    continue
                }
            }
            return encoder.Encode(wire.LogEvent{Type: "end", Record: record, Log: wire.LogChunk{Offset: next, Cursor: cursor}})
        }
        if chunk.Truncated {
            continue
        }
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-time.After(150 * time.Millisecond):
        }
    }
}

func readLogRequest(req wire.Request, token string) (wire.LogChunk, error) {
    var expected *wire.LogCursor
    var offset *int64
    if token != "" {
        c, err := wire.DecodeLogCursor(token)
        if err != nil || !c.Matches(req) {
            return wire.LogChunk{}, errors.New("BAD_LOG_CURSOR")
        }
        expected, offset = &c, &c.Offset
    }
    return readLogFile(filepath.Join(req.Root, "runs", req.RunID), req.Tail, offset, req, expected)
}

// The boundary digest is required for log continuity, including a truncate and
// rewrite that has already grown past the saved position. It examines only the
// 64 bytes preceding that position, not whole logs on every observation.
func logBoundary(f *os.File, offset int64) (string, error) {
    if offset == 0 {
        return "", nil
    }
    data := make([]byte, min(offset, int64(64)))
    n, err := f.ReadAt(data, offset-int64(len(data)))
    if err != nil || n != len(data) {
        return "", errors.New("LOG_CHANGED")
    }
    sum := sha256.Sum256(data)
    return hex.EncodeToString(sum[:]), nil
}

func readLogFile(dir string, tail int, offset *int64, req wire.Request, expected *wire.LogCursor) (result wire.LogChunk, err error) {
    f, err := os.OpenFile(filepath.Join(dir, "output.log"), os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
    if errors.Is(err, os.ErrNotExist) {
        if expected != nil && expected.FileID != "" {
            return result, errors.New("LOG_CHANGED")
        }
        result.Cursor, err = wire.EncodeLogCursor(wire.LogCursor{FormatVersion: 1, Version: req.Version, Root: req.Root, ProjectID: req.ProjectID, RunID: req.RunID})
        return result, err
    }
    if err != nil {
        return result, errors.New("LOG_READ_FAILED")
    }
    defer f.Close()
    info, err := f.Stat()
    if err != nil || !info.Mode().IsRegular() {
        return result, errors.New("LOG_READ_FAILED")
    }
    identity, ok := info.Sys().(*syscall.Stat_t)
    if !ok {
        return result, errors.New("LOG_READ_FAILED")
    }
    fileID := fmt.Sprintf("%d:%d", identity.Dev, identity.Ino)
    if expected != nil && expected.FileID != "" {
        if expected.FileID != fileID {
            return result, errors.New("LOG_CHANGED")
        }
        boundary, err := logBoundary(f, expected.Offset)
        if err != nil || boundary != expected.Boundary {
            return result, errors.New("LOG_CHANGED")
        }
    }
    defer func() {
        if err != nil {
            return
        }
        boundary, boundaryErr := logBoundary(f, result.Offset)
        if boundaryErr != nil {
            err = boundaryErr
            return
        }
        result.Cursor, err = wire.EncodeLogCursor(wire.LogCursor{FormatVersion: 1, Version: req.Version, Root: req.Root, ProjectID: req.ProjectID, RunID: req.RunID, FileID: fileID, Offset: result.Offset, Boundary: boundary})
    }()
    size := info.Size()
    start := int64(0)
    if offset != nil {
        start = *offset
        if start < 0 || start > size {
            return result, errors.New("LOG_CHANGED")
        }
    } else {
        if tail == 0 {
            return wire.LogChunk{Offset: size, Truncated: size > 0}, nil
        }
        if size > logLimit {
            start = size - logLimit
        }
    }
    data := make([]byte, min(int64(logLimit), size-start))
    n, err := f.ReadAt(data, start)
    if err != nil && !errors.Is(err, io.EOF) {
        return result, errors.New("LOG_READ_FAILED")
    }
    data = data[:n]
    // Keep UTF-8 code points together across successive streaming chunks.
    if offset != nil && start+int64(n) < size && len(data) > 0 {
        for i := len(data) - 1; i >= max(0, len(data)-utf8.UTFMax); i-- {
            if utf8.RuneStart(data[i]) {
                if !utf8.FullRune(data[i:]) {
                    data = data[:i]
                    n = i
                }
                break
            }
        }
    }
    result.Offset = start + int64(n)
    if offset == nil {
        // Drop a partial first line when there is a later boundary. A single
        // oversized line still yields a bounded suffix rather than no output.
        if start > 0 {
            if i := bytes.IndexByte(data, '\n'); i >= 0 && i < len(data)-1 {
                data = data[i+1:]
            }
        }
        end := len(data)
        if end > 0 && data[end-1] == '\n' {
            end--
        }
        count := 0
        for i := end - 1; i >= 0; i-- {
            if data[i] == '\n' {
                count++
                if count == tail {
                    data = data[i+1:]
                    break
                }
            }
        }
        result.Truncated = int64(len(data)) < size
    } else {
        result.Truncated = result.Offset < size
    }
    result.Text = string(data)
    return result, nil
}
