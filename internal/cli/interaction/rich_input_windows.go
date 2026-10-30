package interaction

import (
    "context"
    "io"
    "os"
    "sync"
    "time"

    tea "charm.land/bubbletea/v2"
    "golang.org/x/sys/windows"
    "golang.org/x/term"
)

// Keep the terminal fd for Bubble Tea's raw/VT modes, but deliberately omit
// Name. Ultraviolet's Windows InputRecord serializer borrows a mutable buffer
// across goroutines. Its generic VT reader instead owns each byte chunk.
// Loaf joins cancellable native reads before returning stdin to another owner.
type richConsoleInput struct {
    file   *os.File
    ctx    context.Context
    cancel context.CancelFunc
    mu     sync.Mutex
    closed bool
    reads  sync.WaitGroup
}

func richInput(input io.Reader) (io.Reader, func()) {
    file, ok := input.(*os.File)
    if !ok {
        return input, func() {}
    }
    var mode uint32
    if windows.GetConsoleMode(windows.Handle(file.Fd()), &mode) != nil {
        return input, func() {}
    }
    ctx, cancel := context.WithCancel(context.Background())
    reader := &richConsoleInput{file: file, ctx: ctx, cancel: cancel}
    return reader, func() { _ = reader.Close() }
}

func (r *richConsoleInput) Fd() uintptr                 { return r.file.Fd() }
func (r *richConsoleInput) Write(p []byte) (int, error) { return r.file.Write(p) }

func (r *richConsoleInput) Read(p []byte) (int, error) {
    r.mu.Lock()
    if r.closed {
        r.mu.Unlock()
        return 0, os.ErrClosed
    }
    r.reads.Add(1)
    r.mu.Unlock()
    defer r.reads.Done()
    return terminalRead(r.ctx, r.file, func() (int, error) { return r.file.Read(p) })
}

// Close cancels only reads owned by this form, never closes the borrowed stdin.
func (r *richConsoleInput) Close() error {
    r.mu.Lock()
    r.closed = true
    r.cancel()
    r.mu.Unlock()
    r.reads.Wait()
    return nil
}

// VT byte reads do not deliver WINDOW_BUFFER_SIZE_EVENT records. Observe the
// owned output console while this program runs, preserving live resize events.
func watchRichSize(program *tea.Program, output io.Writer) func() {
    file, ok := output.(interface{ Fd() uintptr })
    if !ok {
        return func() {}
    }
    width, height, err := term.GetSize(int(file.Fd()))
    if err != nil {
        return func() {}
    }
    ctx, cancel := context.WithCancel(context.Background())
    done := make(chan struct{})
    go func() {
        defer close(done)
        tick := time.NewTicker(150 * time.Millisecond)
        defer tick.Stop()
        for {
            select {
            case <-ctx.Done():
                return
            case <-tick.C:
                w, h, err := term.GetSize(int(file.Fd()))
                if err == nil && w > 0 && h > 0 && (w != width || h != height) {
                    width, height = w, h
                    program.Send(tea.WindowSizeMsg{Width: w, Height: h})
                }
            }
        }
    }()
    // Program.Run cancels its message channel before returning, so a pending
    // Send also finishes before this command releases its terminal owner.
    return func() { cancel(); <-done }
}
