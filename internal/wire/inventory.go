package wire

import "time"

// OutputEntry exposes metadata only, relative to the frozen run work directory.
type OutputEntry struct {
    Path       string    `json:"path"`
    Type       string    `json:"type"`
    Size       int64     `json:"size"`
    ModifiedAt time.Time `json:"modified_at"`
}

// OutputInventory is a bounded, live listing. Coverage is explicit rather than
// an estimate of the unvisited tree. Links are never traversed.
type OutputInventory struct {
    Entries       []OutputEntry `json:"entries"`
    Missing       []string      `json:"missing"`
    Examined      int           `json:"examined"`
    RootsExamined int           `json:"roots_examined"`
    RootsTotal    int           `json:"roots_total"`
    Depth         int           `json:"depth"`
    Limit         int           `json:"limit"`
    Truncated     bool          `json:"truncated"`
    DepthLimited  int           `json:"depth_limited"`
}
