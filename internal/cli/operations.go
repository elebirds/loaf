package cli

import (
    "encoding/base64"
    "encoding/json"
    "sync"
    "unicode/utf8"

    "github.com/elebirds/loaf/internal/ssh"
)

type execResult struct {
    Type   string `json:"type"`
    Target string `json:"target"`
    ssh.ExecResult
}

type execChunk struct {
    Type     string `json:"type"`
    Stream   string `json:"stream"`
    Offset   int64  `json:"offset"`
    Encoding string `json:"encoding"`
    Data     string `json:"data"`
}

// execOutput bounds every NDJSON chunk while preserving arbitrary program bytes.
// One shared lock serializes the encoder; offsets are independent per stream.
type execOutput struct {
    encoder  *json.Encoder
    lock     *sync.Mutex
    stream   string
    offset   int64
    writeErr *error
}

func (w *execOutput) Write(data []byte) (int, error) {
    w.lock.Lock()
    defer w.lock.Unlock()
    if w.writeErr != nil && *w.writeErr != nil {
        return 0, *w.writeErr
    }
    written := 0
    for len(data) > 0 {
        n := min(len(data), 32<<10)
        chunk := data[:n]
        event := execChunk{Type: "chunk", Stream: w.stream, Offset: w.offset, Encoding: "utf-8", Data: string(chunk)}
        if !utf8.Valid(chunk) {
            event.Encoding = "base64"
            event.Data = base64.StdEncoding.EncodeToString(chunk)
        }
        if err := w.encoder.Encode(envelope{SchemaVersion: 1, OK: true, Data: event}); err != nil {
            if w.writeErr != nil {
                *w.writeErr = err
            }
            return written, err
        }
        w.offset += int64(n)
        written += n
        data = data[n:]
    }
    return written, nil
}
