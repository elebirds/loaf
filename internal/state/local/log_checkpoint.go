package local

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "io"
    "os"
    "path/filepath"

    "github.com/elebirds/loaf/internal/atomicfile"
    "github.com/elebirds/loaf/internal/fault"
    "github.com/elebirds/loaf/internal/filelock"
    "github.com/elebirds/loaf/internal/privatefs"
    "github.com/elebirds/loaf/internal/wire"
)

type logCheckpoint struct {
    FormatVersion int    `json:"format_version"`
    Cursor        string `json:"cursor"`
}

// ReadLogCheckpoint reads an acknowledged opaque cursor without creating state.
// Existing symlinks, oversized files and unknown formats are never overwritten.
func ReadLogCheckpoint(path string) (string, error) {
    info, err := os.Lstat(path)
    if errors.Is(err, os.ErrNotExist) {
        return "", nil
    }
    if err != nil {
        return "", err
    }
    if !info.Mode().IsRegular() || info.Size() > 16384 {
        return "", fault.New("BAD_CONFIG", "log_checkpoint_invalid", nil)
    }
    data, err := os.ReadFile(path)
    if err != nil {
        return "", err
    }
    var r logCheckpoint
    d := json.NewDecoder(bytes.NewReader(data))
    d.DisallowUnknownFields()
    if d.Decode(&r) != nil || r.FormatVersion != 1 || d.Decode(new(any)) != io.EOF {
        return "", fault.New("BAD_CONFIG", "log_checkpoint_invalid", nil)
    }
    if _, err := wire.DecodeLogCursor(r.Cursor); err != nil {
        return "", fault.New("BAD_CONFIG", "log_checkpoint_invalid", nil)
    }
    return r.Cursor, nil
}

// SaveLogCheckpoint atomically acknowledges a processed chunk. A checkpoint can
// advance only within the same bound stream; stale consumers cannot rewind it.
func SaveLogCheckpoint(ctx context.Context, path, token string) error {
    next, err := wire.DecodeLogCursor(token)
    if err != nil {
        return fault.New("BAD_CONFIG", "log_cursor_invalid", nil)
    }
    return checkpointLocked(ctx, path, func() error {
        previous, err := ReadLogCheckpoint(path)
        if err != nil {
            return err
        }
        if previous != "" {
            old, _ := wire.DecodeLogCursor(previous)
            if next.Version != old.Version || next.Root != old.Root || next.ProjectID != old.ProjectID || next.RunID != old.RunID || next.Offset < old.Offset || (old.FileID != "" && next.FileID != old.FileID) {
                return fault.New("BAD_CONFIG", "log_checkpoint_invalid", nil)
            }
        }
        data, err := json.MarshalIndent(logCheckpoint{1, token}, "", "    ")
        if err != nil {
            return err
        }
        return atomicfile.Write(path, append(data, '\n'))
    })
}

func checkpointLocked(ctx context.Context, path string, fn func() error) error {
    if err := privatefs.MkdirAll(filepath.Dir(path)); err != nil {
        return err
    }
    f, err := filelock.Acquire(ctx, path+".lock", -1)
    if err != nil {
        return err
    }
    defer f.Close()
    return fn()
}
