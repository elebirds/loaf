//go:build !windows

package ssh

import (
    "bufio"
    "context"
    "io"
    "net"
    "os"
    "path/filepath"
    "strings"
    "time"

    "github.com/elebirds/loaf/internal/fault"
)

// NativeAskpassInvocation is false on Unix, whose private launcher supplies a subcommand.
func NativeAskpassInvocation(args []string) bool { return false }

// passwordPrompt uses OpenSSH's native askpass hook, not a stream relay. The
// secret is never written to a file, command argument or environment variable.
// The private socket and secret-free launcher exist only during this subprocess.
func passwordPrompt(ctx context.Context, password, executable string) ([]string, func(), error) {
    if executable == "" {
        var err error
        executable, err = os.Executable()
        if err != nil {
            return nil, nil, err
        }
    }
    if !filepath.IsAbs(executable) || strings.ContainsAny(password, "\x00\r\n") {
        return nil, nil, fault.New("BAD_CONFIG", "cloud_config", nil)
    }
    // macOS limits Unix socket paths to 104 bytes; its default TMPDIR can
    // exceed that once a socket filename is appended.
    dir, err := os.MkdirTemp("/tmp", "loaf-askpass-")
    if err != nil {
        return nil, nil, err
    }
    address := filepath.Join(dir, "socket")
    listener, err := net.Listen("unix", address)
    if err != nil {
        _ = os.RemoveAll(dir)
        return nil, nil, err
    }
    launcher := filepath.Join(dir, "askpass")
    if err := os.WriteFile(launcher, []byte("#!/bin/sh\nexec "+Quote(executable)+" __ssh-askpass \"$@\"\n"), 0700); err != nil {
        listener.Close()
        _ = os.RemoveAll(dir)
        return nil, nil, err
    }
    done := make(chan struct{})
    go func() {
        select {
        case <-ctx.Done():
            listener.Close()
        case <-done:
        }
    }()
    stopped := make(chan struct{})
    go func() {
        defer close(stopped)
        for {
            connection, err := listener.Accept()
            if err != nil {
                return
            }
            _ = connection.SetDeadline(time.Now().Add(5 * time.Second))
            prompt, _ := bufio.NewReader(io.LimitReader(connection, 4096)).ReadString('\n')
            lower := strings.ToLower(prompt)
            // Never answer host-key confirmation, key passphrases or arbitrary
            // keyboard-interactive challenges with the instance password.
            if strings.Contains(lower, "password:") && !strings.Contains(lower, "passphrase") && !strings.Contains(lower, "yes/no") {
                _, _ = io.WriteString(connection, password+"\n")
            }
            _ = connection.Close()
        }
    }()
    cleanup := func() { close(done); listener.Close(); <-stopped; _ = os.RemoveAll(dir) }
    return []string{"SSH_ASKPASS=" + launcher, "SSH_ASKPASS_REQUIRE=force", "LOAF_ASKPASS_SOCKET=" + address}, cleanup, nil
}

// AskpassMain is a private native-SSH helper. It emits only an approved password
// prompt's response; it cannot approve host keys or run remote commands.
func AskpassMain(prompt string, out io.Writer) int {
    address := os.Getenv("LOAF_ASKPASS_SOCKET")
    if address == "" || strings.ContainsAny(prompt, "\r\n\x00") {
        return 1
    }
    connection, err := net.DialTimeout("unix", address, time.Second)
    if err != nil {
        return 1
    }
    defer connection.Close()
    _ = connection.SetDeadline(time.Now().Add(5 * time.Second))
    if _, err := io.WriteString(connection, prompt+"\n"); err != nil {
        return 1
    }
    value, err := io.ReadAll(io.LimitReader(connection, 4096))
    if err != nil || len(value) == 0 {
        return 1
    }
    if _, err := out.Write(value); err != nil {
        return 1
    }
    return 0
}
