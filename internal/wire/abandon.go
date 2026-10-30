package wire

import "time"

// AbandonReceipt proves that preparation can no longer be launched. It is
// independent of an execution outcome and survives removal of the payload.
type AbandonReceipt struct {
    FormatVersion  int        `json:"format_version"`
    Version        string     `json:"version"`
    Root           string     `json:"root"`
    ProjectID      string     `json:"project_id"`
    RunID          string     `json:"run_id"`
    NoStartAt      *time.Time `json:"no_start_at,omitempty"`
    CleanedAt      *time.Time `json:"cleaned_at,omitempty"`
    PayloadPresent bool       `json:"payload_present"`
    Device         uint64     `json:"device,omitempty"`
    Inode          uint64     `json:"inode,omitempty"`
}

// Valid checks the identity-independent receipt fields. Preview receipts do
// not yet prove no-start; a completed deletion always does.
func (r AbandonReceipt) Valid() bool {
    return r.FormatVersion == 1 && r.Version != "" &&
        (r.NoStartAt == nil || !r.NoStartAt.IsZero()) &&
        (r.CleanedAt == nil || !r.CleanedAt.IsZero() && r.NoStartAt != nil && !r.CleanedAt.Before(*r.NoStartAt)) &&
        (r.PayloadPresent && r.Inode != 0 || !r.PayloadPresent && r.Inode == 0 && r.Device == 0)
}
