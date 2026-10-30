//go:build !windows

package interaction

import (
    tea "charm.land/bubbletea/v2"
    "context"
    "io"
)

func richInput(_ context.Context, input io.Reader) (io.Reader, func(), error) {
    return input, func() {}, nil
}

func watchRichSize(_ *tea.Program, _ io.Writer) func() { return func() {} }
