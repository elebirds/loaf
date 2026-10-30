//go:build !windows

package interaction

import (
    tea "charm.land/bubbletea/v2"
    "io"
)

func richInput(input io.Reader) (io.Reader, func()) { return input, func() {} }

func watchRichSize(_ *tea.Program, _ io.Writer) func() { return func() {} }
