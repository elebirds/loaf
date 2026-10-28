# Deliver a fact to an explicitly selected Agent

Use this Go module when a background operation finishes and its result should
reach a particular coding session. It does not need Loaf, a database, a queue,
or a model API key of its own. Native Agent authentication stays with the Agent.

```go
sender := agentdelivery.Client{}
destination := agentdelivery.Destination{
    Kind: "codex",
    SessionID: "EXPLICIT-THREAD-ID",
    Endpoint: "/private/path/to/app-server.sock",
}
receipt, err := sender.Deliver(ctx, destination, agentdelivery.Message{
    ID: "result_123", // stable across observation/recovery
    Subject: "file processing completed",
    Text: "The requested export is ready for inspection.",
})
```

Save the receipt alongside the operation that generated the message. A send
that returns `unconfirmed` must not be blindly repeated: it may have already
entered the Agent's queue. The SDK itself never retries or opens another session.

## Supported modes

| Kind | Mechanism | Strongest automatic evidence |
| --- | --- | --- |
| `codex` | The native `codex app-server proxy` connects to an already running local App Server; a standalone tool result targets the exact loaded thread | `accepted` with an App Server turn ID |
| `claude-channel` | An explicitly installed MCP Channel helper receives a private Unix-socket message and pushes a channel notification | `written`; `processed` only after the Agent calls `ack_event` |
| `webhook` | One JSON POST to the configured HTTP/HTTPS endpoint, without redirects | HTTP destination `accepted`, not Agent processing |

`claude-resume` is deliberately not an automatic fallback. Existing desktop
sessions, SDK-managed sessions and separate headless sessions have different
ownership and permission contracts.

### Codex

The adapter reuses `github.com/pmenglund/codex-sdk-go/rpc` at `v0.164.0` for
bounded JSON-RPC, connection ownership and cancellation. Its upstream license
is Apache-2.0. Probe reads the selected thread only. Deliver uses the public
standalone `toolOutput` contract; it does not steer an active turn, create a
thread, change its workspace or override sandbox/approval settings.

Set `Client.CodexBinary` when the native executable is not on PATH. An omitted
endpoint selects the native proxy's default control socket, not a new server.
The selected thread must already be loaded in that server. `Probe` does not
start a model turn.

The public protocol is documented in
[OpenAI's App Server reference](https://learn.chatgpt.com/docs/app-server).
Real desktop versions must be accepted separately from SDK-owned test sessions.

### Claude Channel

Build the optional helper from `examples/channel` and explicitly register it
as the channel for the desired Claude session. Give it a short absolute Unix
socket path in a private `0700` directory and a unique session binding label:

```text
agentdelivery-channel --socket /private/short-directory/channel.sock --session research-a
```

The destination then uses `Kind: "claude-channel"`, the same socket path and
`SessionID: "research-a"`. The helper exposes only the channel notification and
`ack_event` tool. It does not proxy permission approvals or offer workload tools.

This implementation reuses the official MCP Go SDK `v1.6.1`, whose negotiated
protocol is compatible with the documented channel limitation. The helper's
receipt cache is bounded and process-local. It reports `written` after the
transport write; a caller may use `Lookup` to obtain a later explicit ACK.
After a helper restart an absent receipt remains unknown, not safely replayable.

Claude Channels are a preview capability with account/organization/plugin
admission conditions. The helper must be allowed and explicitly enabled in the
target session. This SDK does not turn off those controls or use a development
bypass. See [Channels](https://code.claude.com/docs/en/channels) and the
[notification/ACK contract](https://code.claude.com/docs/en/channels-reference).

Embedding programs can call `RunClaudeChannel` with their own dedicated
`io.ReadCloser` input and `io.Writer` output. The helper closes its protocol
input on exit to unblock reads; it leaves output open and emits no ordinary
CLI text. The example uses `os.Stdin` and `os.Stdout` for that dedicated process.

## Receipt stages

- `rejected`: a confirmed rejection; retry only when `RetrySafe` is true.
- `written`: notification entered a transport; acceptance/processing is unknown.
- `accepted`: the intended destination explicitly accepted it.
- `processed`: a correlated Agent acknowledgement was observed.
- `unconfirmed`: the send may have happened; retain its stable identity.

`Capabilities` describes the adapter's evidence mechanisms. It does not promise
that a destination is always online or that the Agent will complete subsequent
work. Do not put credentials or private workload environments into messages.

## Build and test independently

```text
cd sdk/agentdelivery
GOWORK=off go test ./...
GOWORK=off go build -o agentdelivery-channel ./examples/channel
```

`examples/send` is an ordinary Go consumer of this SDK. It imports no Loaf
package and leaves retry/persistence decisions to its caller. No example runs
an experiment, acquires a machine, or changes an Agent's permissions.
