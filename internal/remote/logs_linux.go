package remote

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "io"
    "os"
    "path/filepath"
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
    log, err := readLog(filepath.Join(req.Root, "runs", req.RunID), req.Tail, nil)
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
    var offset *int64
    encoder := json.NewEncoder(out)
    lastPhase, lastStatus := "", ""
    for {
        record, err := observationStatus(req)
        if err != nil {
            return err
        }
        chunk, err := readLog(filepath.Join(req.Root, "runs", req.RunID), req.Tail, offset)
        if err != nil {
            return err
        }
        next := chunk.Offset
        changed := lastPhase != record.Phase || lastStatus != record.Status
        if offset == nil || chunk.Text != "" || changed {
            if err := encoder.Encode(wire.LogEvent{Type: "chunk", Record: record, Log: chunk}); err != nil {
                return err
            }
        }
        lastPhase, lastStatus = record.Phase, record.Status
        offset = &next
        ended := terminal(record.Status) || record.Status == "unknown" || record.SupervisorPID == 0
        expired := !deadline.IsZero() && !time.Now().Before(deadline)
        if ended || expired {
            // Terminal records are committed after log.Sync. Drain all remaining
            // bytes before emitting end, including a final chunk over the limit.
            final, err := readLog(filepath.Join(req.Root, "runs", req.RunID), req.Tail, offset)
            if err != nil {
                return err
            }
            if final.Text != "" {
                if err := encoder.Encode(wire.LogEvent{Type: "chunk", Record: record, Log: final}); err != nil {
                    return err
                }
                next = final.Offset
                offset = &next
                if final.Truncated {
                    continue
                }
            }
            return encoder.Encode(wire.LogEvent{Type: "end", Record: record, Log: wire.LogChunk{Offset: *offset}})
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

func readLog(dir string, tail int, offset *int64) (wire.LogChunk, error) {
    result := wire.LogChunk{}
    f, err := os.OpenFile(filepath.Join(dir, "output.log"), os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
    if errors.Is(err, os.ErrNotExist) {
        return result, nil
    }
    if err != nil {
        return result, errors.New("LOG_READ_FAILED")
    }
    defer f.Close()
    info, err := f.Stat()
    if err != nil || !info.Mode().IsRegular() {
        return result, errors.New("LOG_READ_FAILED")
    }
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
