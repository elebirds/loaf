package agentdelivery

import (
    "context"
    "net"
    "net/http"
    "os"
    "path"
    "path/filepath"
    "runtime"
    "time"

    "github.com/coder/websocket"
    "github.com/pmenglund/codex-sdk-go/rpc"
)

// The public Unix transport carries WebSocket frames, not raw JSONL. Keep the
// small framing adapter here while the existing RPC library owns JSON-RPC.
type codexSocket struct{ connection *websocket.Conn }

func openCodexSocket(ctx context.Context, endpoint string) (rpc.Transport, error) {
    if runtime.GOOS == "windows" {
        return nil, ErrUnsupported
    }
    if endpoint == "" {
        home := os.Getenv("CODEX_HOME")
        if home == "" {
            var err error
            home, err = os.UserHomeDir()
            if err != nil {
                return nil, ErrUnavailable
            }
            home = filepath.Join(home, ".codex")
        }
        endpoint = filepath.Join(home, "app-server-control", "app-server-control.sock")
    }
    if !path.IsAbs(endpoint) {
        return nil, ErrDestination
    }
    if err := privateSocket(endpoint, true); err != nil {
        return nil, err
    }
    transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
        return (&net.Dialer{}).DialContext(ctx, "unix", endpoint)
    }}
    defer transport.CloseIdleConnections()
    client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
    connect, cancel := context.WithTimeout(ctx, 20*time.Second)
    defer cancel()
    connection, response, err := websocket.Dial(connect, "ws://localhost/", &websocket.DialOptions{HTTPClient: client, CompressionMode: websocket.CompressionDisabled})
    if err != nil {
        if response != nil && response.Body != nil {
            response.Body.Close()
        }
        return nil, ErrUnavailable
    }
    connection.SetReadLimit(1 << 20)
    return codexSocket{connection}, nil
}

func (s codexSocket) ReadLine() (string, error) {
    _, data, err := s.connection.Read(context.Background())
    return string(data), err
}

func (s codexSocket) WriteLine(line string) error {
    ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
    defer cancel()
    return s.WriteLineContext(ctx, line)
}

func (s codexSocket) WriteLineContext(ctx context.Context, line string) error {
    return s.connection.Write(ctx, websocket.MessageText, []byte(line))
}

func (s codexSocket) Close() error { return s.connection.CloseNow() }
