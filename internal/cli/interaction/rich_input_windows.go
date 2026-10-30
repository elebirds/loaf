package interaction

import (
    "context"
    "fmt"
    "io"
    "os"
    "sync"
    "time"
    "unicode/utf16"
    "unicode/utf8"

    tea "charm.land/bubbletea/v2"
    xwindows "github.com/charmbracelet/x/windows"
    "golang.org/x/sys/windows"
    "golang.org/x/term"
)

// Keep the terminal fd for Bubble Tea's raw/VT modes, but deliberately omit
// Name. Ultraviolet's Windows InputRecord serializer borrows a mutable buffer
// across goroutines. Its generic VT reader instead owns each byte chunk.
// Loaf joins cancellable native reads before returning stdin to another owner.
type richConsoleInput struct {
    file      *os.File
    ctx       context.Context
    cancel    context.CancelFunc
    mu        sync.Mutex
    closed    bool
    reads     sync.WaitGroup
    stream    sync.Mutex
    pending   []byte
    half      rune
    canceled  windows.Handle
    stop      func() bool
    finished  chan struct{}
    closeOnce sync.Once
    closeErr  error
}

func richInput(parent context.Context, input io.Reader) (io.Reader, func(), error) {
    file, ok := input.(*os.File)
    if !ok {
        return input, func() {}, nil
    }
    var mode uint32
    if windows.GetConsoleMode(windows.Handle(file.Fd()), &mode) != nil {
        return input, func() {}, nil
    }
    event, err := windows.CreateEvent(nil, 1, 0, nil)
    if err != nil {
        return nil, nil, err
    }
    ctx, cancel := context.WithCancel(parent)
    reader := &richConsoleInput{file: file, ctx: ctx, cancel: cancel, canceled: event, finished: make(chan struct{})}
    reader.stop = context.AfterFunc(ctx, func() { windows.SetEvent(event); close(reader.finished) })
    return reader, func() { _ = reader.Close() }, nil
}

func (r *richConsoleInput) Fd() uintptr                 { return r.file.Fd() }
func (r *richConsoleInput) Write(p []byte) (int, error) { return r.file.Write(p) }

func (r *richConsoleInput) Read(p []byte) (int, error) {
    r.mu.Lock()
    if r.closed {
        r.mu.Unlock()
        return 0, io.EOF
    }
    r.reads.Add(1)
    r.mu.Unlock()
    defer r.reads.Done()
    r.stream.Lock()
    defer r.stream.Unlock()
    if len(p) == 0 {
        return 0, nil
    }
    for len(r.pending) == 0 {
        handle := windows.Handle(r.file.Fd())
        // Never abort a pending ReadConsole: Windows can retain its cooked
        // editing state and consume the next native prompt's first input.
        // This session is the sole reader, so readiness admits one record.
        result, err := windows.WaitForMultipleObjects([]windows.Handle{handle, r.canceled}, false, windows.INFINITE)
        if r.ctx.Err() != nil {
            return 0, io.EOF
        }
        if err != nil {
            return 0, err
        }
        if result != windows.WAIT_OBJECT_0 {
            return 0, io.ErrUnexpectedEOF
        }
        var record xwindows.InputRecord
        var count uint32
        if err := xwindows.ReadConsoleInput(handle, &record, 1, &count); err != nil {
            return 0, err
        }
        if count == 0 {
            return 0, io.EOF
        }
        if record.EventType != xwindows.KEY_EVENT {
            continue
        }
        key := record.KeyEvent()
        modifiers := uint32(xwindows.SHIFT_PRESSED | xwindows.LEFT_CTRL_PRESSED | xwindows.RIGHT_CTRL_PRESSED | xwindows.LEFT_ALT_PRESSED | xwindows.RIGHT_ALT_PRESSED)
        if key.Char == 0 || key.Char < 0x80 && key.ControlKeyState&modifiers != 0 {
            // Preserve native navigation/function keys and their modifiers.
            if key.VirtualKeyCode != 0 {
                down := 0
                if key.KeyDown {
                    down = 1
                }
                r.pending = fmt.Appendf(r.pending, "\x1b[%d;%d;%d;%d;%d;%d_", key.VirtualKeyCode, key.VirtualScanCode, key.Char, down, key.ControlKeyState, key.RepeatCount)
            }
            continue
        }
        // ConPTY/Alt Unicode input can carry its character on Alt release.
        if !key.KeyDown && key.VirtualKeyCode != 0x12 {
            continue
        }
        for repeat := uint16(0); repeat < key.RepeatCount; repeat++ {
            r.appendUnit(key.Char)
        }
    }
    if r.ctx.Err() != nil {
        return 0, io.EOF
    }
    n := copy(p, r.pending)
    r.pending = r.pending[n:]
    return n, nil
}

func (r *richConsoleInput) appendUnit(unit rune) {
    if r.half != 0 {
        high := r.half
        r.half = 0
        if unit >= 0xdc00 && unit <= 0xdfff {
            r.pending = utf8.AppendRune(r.pending, utf16.DecodeRune(high, unit))
            return
        }
        r.pending = utf8.AppendRune(r.pending, utf8.RuneError)
    }
    if unit >= 0xd800 && unit <= 0xdbff {
        r.half = unit
        return
    }
    if utf16.IsSurrogate(unit) {
        unit = utf8.RuneError
    }
    r.pending = utf8.AppendRune(r.pending, unit)
}

// Close cancels only reads owned by this form, never closes the borrowed stdin.
func (r *richConsoleInput) Close() error {
    r.closeOnce.Do(func() {
        r.mu.Lock()
        r.closed = true
        r.cancel()
        r.mu.Unlock()
        r.reads.Wait()
        if !r.stop() {
            <-r.finished
        }
        clear(r.pending)
        r.closeErr = windows.CloseHandle(r.canceled)
    })
    return r.closeErr
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
