package interaction

import (
    "context"
    "errors"
    "io"
    "os"
    "runtime"
    "time"
    "unicode/utf16"
    "unicode/utf8"
    "unsafe"

    "golang.org/x/sys/windows"
)

var cancelSynchronousIO = windows.NewLazySystemDLL("kernel32.dll").NewProc("CancelSynchronousIo")

type terminalResult[T any] struct {
    value T
    err   error
}

// The dedicated OS thread lets cancellation interrupt a synchronous console or
// file read without closing stdin, replacing the terminal, or leaving a reader
// alive after the input session has been released.
func readLine(ctx context.Context, input io.Reader, maxBytes int) ([]byte, error) {
    file, ok := input.(*os.File)
    if !ok {
        return readLineBytes(ctx, input, maxBytes)
    }
    value, err := terminalRead(ctx, file, func() ([]byte, error) {
        var mode uint32
        if windows.GetConsoleMode(windows.Handle(file.Fd()), &mode) == nil {
            return consoleLine(windows.Handle(file.Fd()), maxBytes)
        }
        return readLineBytes(ctx, input, maxBytes)
    })
    if err != nil {
        clear(value)
        return nil, err
    }
    return value, nil
}

func terminalRead[T any](ctx context.Context, file *os.File, read func() (T, error)) (value T, err error) {
    if err := ctx.Err(); err != nil {
        return value, err
    }
    handles := make(chan windows.Handle, 1)
    done := make(chan terminalResult[T], 1)
    go func() {
        runtime.LockOSThread()
        defer runtime.UnlockOSThread()
        thread, err := windows.OpenThread(windows.THREAD_TERMINATE, false, windows.GetCurrentThreadId())
        handles <- thread
        if err != nil {
            done <- terminalResult[T]{err: err}
            return
        }
        var value T
        if err = ctx.Err(); err == nil {
            value, err = read()
        }
        done <- terminalResult[T]{value, err}
    }()
    thread := <-handles
    if thread != 0 {
        defer windows.CloseHandle(thread)
    }
    select {
    case result := <-done:
        if err := ctx.Err(); err != nil {
            return result.value, err
        }
        return result.value, result.err
    case <-ctx.Done():
        // Cancellation can race with entry into the kernel read. Keep canceling
        // until the owned reader acknowledges completion, including that race.
        timer := time.NewTicker(time.Millisecond)
        defer timer.Stop()
        for {
            if thread != 0 {
                cancelSynchronousIO.Call(uintptr(thread))
                windows.CancelIoEx(windows.Handle(file.Fd()), nil)
            }
            select {
            case result := <-done:
                return result.value, ctx.Err()
            case <-timer.C:
            }
        }
    }
}

func readByte(ctx context.Context, input io.Reader, one []byte) (int, error) {
    if err := ctx.Err(); err != nil {
        return 0, err
    }
    n, err := input.Read(one)
    if canceled := ctx.Err(); canceled != nil {
        return 0, canceled
    }
    if n == 0 && err == nil {
        err = io.ErrNoProgress
    }
    return n, err
}

func consoleLine(handle windows.Handle, limit int) (value []byte, err error) {
    units := make([]uint16, limit+2)
    defer clear(units)
    var count uint32
    if err := windows.ReadConsole(handle, &units[0], uint32(len(units)), &count, nil); err != nil {
        return nil, err
    }
    if count == 0 {
        return nil, io.EOF
    }
    if units[count-1] != '\n' {
        return nil, ErrInputTooLong
    }
    count--
    if count > 0 && units[count-1] == '\r' {
        count--
    }
    value = make([]byte, 0, limit)
    defer func() {
        if err != nil {
            clear(value)
            value = nil
        }
    }()
    for i := 0; i < int(count); i++ {
        r := rune(units[i])
        if utf16.IsSurrogate(r) {
            if i+1 < int(count) && 0xd800 <= r && r <= 0xdbff && 0xdc00 <= units[i+1] && units[i+1] <= 0xdfff {
                r = utf16.DecodeRune(r, rune(units[i+1]))
                i++
            } else {
                r = utf8.RuneError
            }
        }
        if len(value)+utf8.RuneLen(r) > limit {
            return value, ErrInputTooLong
        }
        value = utf8.AppendRune(value, r)
    }
    return value, nil
}

// ReadTerminalSecret owns its editing buffer: Windows cooked reads retain an
// unfinished line after cancellation even after FlushConsoleInputBuffer. Reading
// key events prevents that secret from resurfacing in the next visible prompt.
func ReadTerminalSecret(ctx context.Context, input *os.File, maxBytes int) (value []byte, err error) {
    if err := ctx.Err(); err != nil {
        return nil, err
    }
    if input == nil {
        return nil, ErrTerminalRequired
    }
    handle := windows.Handle(input.Fd())
    var original uint32
    if err := windows.GetConsoleMode(handle, &original); err != nil {
        return nil, ErrTerminalRequired
    }
    hidden := original &^ (windows.ENABLE_ECHO_INPUT | windows.ENABLE_LINE_INPUT)
    hidden |= windows.ENABLE_PROCESSED_INPUT
    if err := windows.SetConsoleMode(handle, hidden); err != nil {
        return nil, err
    }
    defer func() {
        var flush error
        if err != nil {
            flush = windows.FlushConsoleInputBuffer(handle)
        }
        restore := windows.SetConsoleMode(handle, original)
        if restore != nil || flush != nil {
            clear(value)
            value = nil
            err = errors.Join(err, restore, flush)
        }
    }()
    return consoleSecretLine(ctx, handle, byteLimit(maxBytes))
}

func consoleSecretLine(ctx context.Context, handle windows.Handle, limit int) ([]byte, error) {
    type keyRecord struct {
        Type    uint16
        Padding uint16
        Down    int32
        Repeat  uint16
        Key     uint16
        Scan    uint16
        Char    uint16
        Control uint32
    }
    read := windows.NewLazySystemDLL("kernel32.dll").NewProc("ReadConsoleInputW")
    canceled, err := windows.CreateEvent(nil, 1, 0, nil)
    if err != nil {
        return nil, err
    }
    finished := make(chan struct{})
    stop := context.AfterFunc(ctx, func() { windows.SetEvent(canceled); close(finished) })
    defer func() {
        if !stop() {
            <-finished
        }
        windows.CloseHandle(canceled)
    }()
    units := make([]uint16, 0, limit+2)
    defer func() { clear(units[:cap(units)]) }()
    cursor := 0
    previous := func() int {
        at := cursor - 1
        if at > 0 && units[at] >= 0xdc00 && units[at] <= 0xdfff && units[at-1] >= 0xd800 && units[at-1] <= 0xdbff {
            at--
        }
        return at
    }
    next := func() int {
        at := cursor + 1
        if at < len(units) && units[cursor] >= 0xd800 && units[cursor] <= 0xdbff && units[at] >= 0xdc00 && units[at] <= 0xdfff {
            at++
        }
        return at
    }
    for {
        // Wait for input or cancellation before starting a read. Aborting a
        // pending console read can consume the first key of the next prompt.
        result, err := windows.WaitForMultipleObjects([]windows.Handle{handle, canceled}, false, windows.INFINITE)
        if err != nil {
            return nil, err
        }
        if err := ctx.Err(); err != nil {
            return nil, err
        }
        if result != windows.WAIT_OBJECT_0 {
            return nil, io.ErrUnexpectedEOF
        }
        var event keyRecord
        var count uint32
        ok, _, err := read.Call(uintptr(handle), uintptr(unsafe.Pointer(&event)), 1, uintptr(unsafe.Pointer(&count)))
        if ok == 0 {
            return nil, err
        }
        if count == 0 {
            return nil, io.EOF
        }
        // Alt-numpad/ConPTY Unicode input carries its character on Alt release.
        if event.Type != 1 || (event.Down == 0 && !(event.Key == 0x12 && event.Char != 0)) {
            continue
        }
        for repeat := uint16(0); repeat < event.Repeat; repeat++ {
            switch {
            case event.Char == '\r' || event.Char == '\n':
                value := make([]byte, 0, limit)
                for i := 0; i < len(units); i++ {
                    r := rune(units[i])
                    if r >= 0xd800 && r <= 0xdbff && i+1 < len(units) && units[i+1] >= 0xdc00 && units[i+1] <= 0xdfff {
                        r = utf16.DecodeRune(r, rune(units[i+1]))
                        i++
                    } else if utf16.IsSurrogate(r) {
                        r = utf8.RuneError
                    }
                    if len(value)+utf8.RuneLen(r) > limit {
                        clear(value)
                        return nil, ErrInputTooLong
                    }
                    value = utf8.AppendRune(value, r)
                }
                return value, nil
            case event.Char == 0x1a:
                return nil, io.EOF
            case event.Key == 0x08 || event.Char == '\b':
                if cursor > 0 {
                    at := previous()
                    copy(units[at:], units[cursor:])
                    units = units[:len(units)-(cursor-at)]
                    cursor = at
                }
            case event.Key == 0x2e && event.Char == 0:
                if cursor < len(units) {
                    at := next()
                    copy(units[cursor:], units[at:])
                    units = units[:len(units)-(at-cursor)]
                }
            case event.Key == 0x24 && event.Char == 0:
                cursor = 0
            case event.Key == 0x23 && event.Char == 0:
                cursor = len(units)
            case event.Key == 0x25 && event.Char == 0:
                if cursor > 0 {
                    cursor = previous()
                }
            case event.Key == 0x27 && event.Char == 0:
                if cursor < len(units) {
                    cursor = next()
                }
            case event.Char >= 0x20:
                if len(units) >= limit+1 {
                    return nil, ErrInputTooLong
                }
                units = append(units, 0)
                copy(units[cursor+1:], units[cursor:len(units)-1])
                units[cursor] = event.Char
                cursor++
            }
        }
    }
}
