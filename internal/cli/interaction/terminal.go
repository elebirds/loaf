package interaction

import (
    "context"
    "errors"
    "io"
)

// readLine does not buffer past a newline. Its fixed backing allocation avoids
// growth copies of secrets, and is cleared on every unsuccessful exit.
func readLineBytes(ctx context.Context, input io.Reader, maxBytes int) (value []byte, err error) {
    buffer := make([]byte, maxBytes+1) // One extra byte permits a CRLF terminator.
    used := 0
    defer func() {
        if err != nil {
            clear(buffer)
        }
    }()
    var one [1]byte
    defer clear(one[:])
    for {
        n, readErr := readByte(ctx, input, one[:])
        if n > 0 {
            if one[0] == '\n' {
                if used > 0 && buffer[used-1] == '\r' {
                    used--
                    buffer[used] = 0
                }
                return buffer[:used], nil
            }
            if used == len(buffer) || (used == maxBytes && one[0] != '\r') {
                return nil, ErrInputTooLong
            }
            buffer[used] = one[0]
            used++
        }
        if readErr != nil {
            if errors.Is(readErr, io.EOF) && used > 0 {
                if buffer[used-1] == '\r' {
                    used--
                    buffer[used] = 0
                }
                if used > maxBytes {
                    return nil, ErrInputTooLong
                }
                return buffer[:used], nil
            }
            return nil, readErr
        }
    }
}
