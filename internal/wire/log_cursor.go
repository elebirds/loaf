package wire

import (
    "bytes"
    "encoding/base64"
    "encoding/json"
    "errors"
    "io"
)

// LogCursor binds a confirmed source-byte position to one version, run and log
// file. Boundary protects continuity across truncation and rewriting; it is not
// an authentication token. Cursors contain no log text or execution environment.
type LogCursor struct {
    FormatVersion int    `json:"format_version"`
    Version       string `json:"version"`
    Root          string `json:"root"`
    ProjectID     string `json:"project_id"`
    RunID         string `json:"run_id"`
    FileID        string `json:"file_id"`
    Offset        int64  `json:"offset"`
    Boundary      string `json:"boundary"`
}

// EncodeLogCursor produces an opaque, bounded navigation token.
func EncodeLogCursor(c LogCursor) (string, error) {
    data, err := json.Marshal(c)
    if err != nil || len(data) > 6144 {
        return "", errors.New("BAD_LOG_CURSOR")
    }
    return base64.RawURLEncoding.EncodeToString(data), nil
}

// DecodeLogCursor rejects unknown formats and malformed tokens without state.
func DecodeLogCursor(token string) (LogCursor, error) {
    var c LogCursor
    if token == "" || len(token) > 8192 {
        return c, errors.New("BAD_LOG_CURSOR")
    }
    data, err := base64.RawURLEncoding.DecodeString(token)
    if err != nil {
        return c, errors.New("BAD_LOG_CURSOR")
    }
    d := json.NewDecoder(bytes.NewReader(data))
    d.DisallowUnknownFields()
    if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.FormatVersion != 1 || c.Version == "" || c.Root == "" || c.ProjectID == "" || c.RunID == "" || c.Offset < 0 || (c.FileID == "" && (c.Offset != 0 || c.Boundary != "")) || (c.Offset > 0 && len(c.Boundary) != 64) {
        return LogCursor{}, errors.New("BAD_LOG_CURSOR")
    }
    return c, nil
}

// Matches prevents applying a cursor to another saved run or software version.
func (c LogCursor) Matches(req Request) bool {
    return c.Version == req.Version && c.Root == req.Root && c.ProjectID == req.ProjectID && c.RunID == req.RunID
}
