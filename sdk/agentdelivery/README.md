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
See [compatibility evidence](COMPATIBILITY.md) for the tested native versions
and the distinction between transport, acceptance and processing results.

## Four adapters are implemented

Native Windows supports the Webhook adapter. The three Unix-socket Agent
adapters and the Claude Channel server return `ErrUnsupported` there, with
`DESTINATION_UNSUPPORTED` where a capability or receipt carries a reason code.
WSL2 uses Linux socket semantics and requires an endpoint reachable and verified
inside that environment; a Windows desktop session is not implicitly accessible.

| Kind | Mechanism | Strongest automatic evidence |
| --- | --- | --- |
| `codex` | A private Unix WebSocket connects to an already running local App Server; a standalone tool result targets the exact loaded thread | `accepted` with an App Server turn ID |
| `codex-desktop` | Private desktop peer IPC discovers the existing owner of the exact thread, then forwards one standalone tool result | `accepted` with the existing thread's turn ID |
| `claude-channel` | An explicitly installed MCP Channel helper receives a private Unix-socket message and pushes a channel notification | `written`; `processed` only after the Agent calls `ack_event` |
| `webhook` | One JSON POST to the configured HTTP/HTTPS endpoint, without redirects | HTTP destination `accepted`, not Agent processing |

### Codex

The adapter reuses `github.com/pmenglund/codex-sdk-go/rpc` at `v0.164.0` for
bounded JSON-RPC, connection ownership and cancellation. Its upstream license
is Apache-2.0. Probe reads the selected thread only. Deliver uses the public
standalone `toolOutput` contract; it does not steer an active turn, create a
thread, change its workspace or override sandbox/approval settings.

The framing adapter uses `github.com/coder/websocket` at `v1.8.14` (ISC license)
for the public WebSocket-over-Unix transport. It needs a running native server,
not a separate model API key. An omitted endpoint selects the native default
control socket under `CODEX_HOME` (or `~/.codex`), not a new server.
The selected thread must already be loaded in that server. `Probe` does not
start a model turn.

The public protocol is documented in
[OpenAI's App Server reference](https://learn.chatgpt.com/docs/app-server).
Real desktop versions must be accepted separately from SDK-owned test sessions.

### Current Codex desktop chat

`codex-desktop` is an explicit experimental adapter for the installed desktop
peer protocol, not an alternative framing of the public App Server API. Supply
the intended `SessionID` and an absolute `Endpoint` such as
`/Users/you/.codex/ipc/ipc.sock`. Loaf's `notify bind NAME --current` resolves
these from the calling tool environment and verifies them before persistence.

The SDK checks current-user ownership and private permissions on the socket and
its parent. A read-only owner-discovery request must confirm the exact local
thread and support for untrusted application input. Delivery targets that owner
with one `toolOutput`; no ordinary user input, thread creation/resume, active
steer, model, workspace or permission override is sent. `CWD` must be empty for
this mode: the destination thread retains its own workspace and settings.

Peer frames are bounded, context-cancellable and versioned. Unrelated bus
messages are ignored, not exposed to the caller. Unsupported owners and protocol
versions fail closed; a possible send never automatically repeats or falls back
to a different server/session. An accepted response is destination evidence,
not an automatic processing acknowledgement.

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

## Explicit Webhook credentials and endpoint policy

`Destination.BearerRef` is an opaque caller-owned reference, not a token. Supply
`Client.Webhook.ResolveBearer` (or `Webhook.ResolveBearer`) to resolve it at send
time. The callback receives context and the reference and returns a transient
token; this SDK does not read environment variables, keyrings, databases or Loaf
configuration for Webhook authentication. Missing/invalid credentials are rejected
before an HTTP send. Never put tokens into a Destination or Message.

HTTPS is the default. HTTP loopback is allowed; other HTTP endpoints require
`AllowInsecureHTTP: true`. Bearer authentication always requires HTTPS. A 429
response is rejected without automatic replay permission, just like other
unproven receiver rejection contracts. Network/5xx ambiguity remains unconfirmed.

All Unix adapters check current-user ownership, private socket and actual parent
directory permissions. Only public Codex transport allows its native short-path
symlink, with the resolved target checked too. Desktop/Claude reject that exception.
`Capabilities.Experimental` identifies desktop peer IPC. The existing-session
and no-policy-override contracts remain unchanged.

Claude's bounded process-local cache evicts processed entries first when full;
it never silently evicts written/unconfirmed evidence. An evicted or restarted
receipt is unknown, not safely replayable. Idempotency is not advertised.
