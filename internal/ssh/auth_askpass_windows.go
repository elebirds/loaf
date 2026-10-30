package ssh

import (
    "bufio"
    "context"
    "crypto/rand"
    "encoding/hex"
    "io"
    "os"
    "path/filepath"
    "strings"
    "sync"
    "time"

    "github.com/Microsoft/go-winio"
    "github.com/elebirds/loaf/internal/fault"
    "golang.org/x/sys/windows"
)

const askpassPipePrefix = `\\.\pipe\loaf-askpass-`

// NativeAskpassInvocation recognizes the native executable invoked directly by
// MSYS2 OpenSSH. No shell launcher or file containing a credential is needed.
func NativeAskpassInvocation(args []string) bool {
    return len(args) == 1 && strings.HasPrefix(os.Getenv("LOAF_ASKPASS_SOCKET"), askpassPipePrefix)
}

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
    user, err := windows.GetCurrentProcessToken().GetTokenUser()
    if err != nil {
        return nil, nil, err
    }
    var nonce [32]byte
    if _, err := rand.Read(nonce[:]); err != nil {
        return nil, nil, err
    }
    address := askpassPipePrefix + hex.EncodeToString(nonce[:])
    // go-winio atomically creates the first pipe instance and rejects remote
    // clients. The protected DACL grants access only to this user's SID.
    sid := user.User.Sid.String()
    listener, err := winio.ListenPipe(address, &winio.PipeConfig{
        SecurityDescriptor: "O:" + sid + "D:P(A;;GA;;;" + sid + ")",
        InputBufferSize:    4096, OutputBufferSize: 4096,
    })
    if err != nil {
        return nil, nil, err
    }
    serving, cancel := context.WithCancel(ctx)
    stopped := make(chan struct{})
    go func() { <-serving.Done(); listener.Close() }()
    go func() {
        defer close(stopped)
        for {
            connection, err := listener.Accept()
            if err != nil {
                return
            }
            connection.SetDeadline(time.Now().Add(5 * time.Second))
            stopClose := context.AfterFunc(serving, func() { connection.Close() })
            prompt, _ := bufio.NewReader(io.LimitReader(connection, 4096)).ReadString('\n')
            lower := strings.ToLower(prompt)
            if strings.Contains(lower, "password:") && !strings.Contains(lower, "passphrase") && !strings.Contains(lower, "yes/no") {
                io.WriteString(connection, password+"\n")
            }
            stopClose()
            connection.Close()
        }
    }()
    var once sync.Once
    cleanup := func() { once.Do(func() { cancel(); listener.Close(); <-stopped }) }
    return []string{"SSH_ASKPASS=" + filepath.ToSlash(executable), "SSH_ASKPASS_REQUIRE=force", "LOAF_ASKPASS_SOCKET=" + address}, cleanup, nil
}

// AskpassMain answers only the private pipe's approved prompts.
func AskpassMain(prompt string, out io.Writer) int {
    address := os.Getenv("LOAF_ASKPASS_SOCKET")
    if !strings.HasPrefix(address, askpassPipePrefix) || strings.ContainsAny(prompt, "\r\n\x00") {
        return 1
    }
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    connection, err := winio.DialPipeContext(ctx, address)
    if err != nil {
        return 1
    }
    defer connection.Close()
    connection.SetDeadline(time.Now().Add(5 * time.Second))
    if _, err := io.WriteString(connection, prompt+"\n"); err != nil {
        return 1
    }
    value, err := io.ReadAll(io.LimitReader(connection, 4096))
    defer clear(value)
    if err != nil || len(value) == 0 {
        return 1
    }
    if _, err := out.Write(value); err != nil {
        return 1
    }
    return 0
}
