package cli

import (
    "io"
    "testing"
)

type uxCoreNoInput struct{ t *testing.T }

func (r uxCoreNoInput) Read([]byte) (int, error) {
    r.t.Fatal("finite command attempted to read stdin")
    return 0, io.EOF
}
