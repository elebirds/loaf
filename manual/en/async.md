# Get results back and share a machine

[Home](../../README.md) · [Guide](guide.md) · [Commands](commands.md) · [简体中文](../zh-Hans/async.md)

Use a notification to deliver a saved result after a task finishes. Use a queue
when cooperating projects share a machine. A direct Run or cloud operation can
notify you without ever entering a queue.

## Send a result to an explicit destination

Register a destination, then name it when submitting work. Replace this example
URL with your receiver:

```bash
loaf notify destination add results --kind webhook --endpoint https://receiver.example/loaf
loaf run --notify results -- python3 train.py
loaf notify serve
```

`notify serve` is a foreground Bridge that collects retained events and delivers
them. Ctrl-C stops this Bridge, not a Run, queue or cloud instance. Keep it running
for timely delivery, or use `loaf notify serve --once` later to process one round.
After local disconnection, retained remote events can be collected on reconnect.

Repeat `--notify` for more destinations. The same flag on `cloud acquire`
subscribes to acquisition, readiness and conditions requiring attention. An
inventory match is not an acquisition; an instance being created does not prove
SSH readiness. A notification does not authorize another purchase or a new task.

Agent bindings address one exact session:

```bash
loaf notify destination add assistant --kind codex --session THREAD_ID --endpoint ENDPOINT
```

Use an endpoint supported by the installed adapter. `codex`, `claude-channel`
and explicitly selected `claude-resume` are separate modes. A saved binding does
not prove reachability or processing capability. Loaf does not choose the latest
session, silently create a replacement, or bypass its workspace and approvals.
Claude Channel delivery requires an open session and platform permission;
headless resume is a separate, explicitly chosen execution mode.

`notify destination list` shows safe metadata, not private connection details.
The standalone Go delivery SDK addresses destinations and reports evidence; it
does not schedule Runs or implement a training runtime.

## Bind an open Claude session with the packaged binary

The optional Channel adapter is part of `loaf`; it needs no Bun, Node or separate
helper installation. Give each open Claude session its own socket and unique
label. Create the socket's parent directory with mode `0700` first. The helper
creates a `0600` socket and rejects an existing socket path.

In that Claude project's `.mcp.json`, replace the absolute paths:

```json
{
  "mcpServers": {
    "loaf-events": {
      "command": "/absolute/path/to/loaf",
      "args": ["notify", "channel", "--socket", "/absolute/private/loaf-channels/research.sock", "--session", "research_1"]
    }
  }
}
```

Claude starts this command as a subprocess. Its stdin/stdout carry native MCP,
not Loaf JSON or NDJSON; `--json` and `--trace` are rejected. Diagnostics use
stderr. The label is this helper's session binding, not an inferred Claude ID.

### Local development validation during preview

Custom Channels currently require Claude's interactive development opt-in. Only
after reviewing this local helper and explicitly choosing to test it:

```bash
claude --dangerously-load-development-channels server:loaf-events
```

This is an optional development test, not normal startup; Loaf never adds the
flag. It cannot override organization `channelsEnabled` or other real denials.
Ordinary use remains subject to upstream admission. See the official
[Channel preview requirements](https://code.claude.com/docs/en/channels-reference#test-during-the-research-preview).

With the session open and permitted, bind exactly the configured socket/label,
then run the separate delivery Bridge:

```bash
loaf notify destination add claude-research --kind claude-channel --endpoint /absolute/private/loaf-channels/research.sock --session research_1
loaf run --notify claude-research -- python3 train.py
loaf notify serve
```

A notification write is only `written`. After reading it, Claude calls the
helper's `ack_event` tool with `{"message_id":"MESSAGE_ID"}` from that message;
only this acknowledgment supplies `processed` evidence. It proves the session
acknowledged reading, not completion of a follow-up task. No ACK leaves delivery
unconfirmed, without an automatic second turn. The adapter exposes no approval
relay; existing permissions still govern any continuation.

## Read facts and delivery evidence separately

```bash
loaf events list --source project --limit 100 --json
loaf events list --source resources --json
loaf notify deliveries --json
loaf notify subscribe run:RUN_ID --destination results
loaf notify subscriptions
```

Sources are `local` (received inbox), `resources` (local cloud-operation facts),
`project` (selected remote project), and `queue` (selected machine queue). Local
and resource sources reject `--target`. `--limit` is 1–1000; `--cursor` belongs to
its original source/database incarnation. There is no global cross-source order.

`--subject run:RUN_ID` filters an exact object. Other kinds are `queue_request`,
`resource_operation` and `resource`; resource facts need no Run ID. Subscriptions
default to `project`; select `--source resources` for cloud facts or `--source
queue` for admission facts. Retained matches are included by default;
`--replay=false` selects future facts only.

`notify subscriptions` keeps the last successful source-sync time and the last
source-query error code/time separately, including after a later successful
sync. Compare those timestamps when checking recovery. A source-query failure
is not a destination rejection or an Agent-processing result.

Delivery records separate application dispatch state from SDK evidence.
`written` means transport output, `accepted` means destination acknowledgment,
and `processed` requires processing evidence. Neither a socket write nor an
ordinary Webhook acceptance proves Agent processing. Ambiguous sends remain
`unconfirmed`, rather than blindly starting another Agent turn.

`notify serve --once --json` returns one finite result. Continuous
`notify serve --json` emits NDJSON deliveries followed by an end/error record.
Progress is not mixed into finite JSON.

## Queue fixed inputs on one machine

```bash
loaf queue enable --slots 1
loaf queue submit --name baseline --notify results -- python3 train.py
loaf queue list
loaf queue show QUEUE_REQUEST_ID
```

Admission freezes a prepared project copy, command, environment and output
selection. Later edits or sync do not change it; external datasets and machine
environment remain external inputs. `queue submit --dry-run` previews locally
without connecting, allocating IDs or writing state.

The remote Coordinator dispatches while this computer is offline; existing
Supervisors own accepted Runs. Projects and aliases share machine/user capacity,
with one slot per request. FIFO orders confirmed admissions, not client clocks.
FIFO is the only implemented policy. Direct Runs and external SSH programs
bypass these cooperative limits.

A request ID tracks admission; its Run ID tracks execution. Queued acceptance
does not prove a workload started. Find the Run with `queue show`, then use its
explicit ID with `status`, `logs`, `stop` and `pull`. Uncertain dispatch is
reconciled against the original Run, not replaced by a second execution.

```bash
loaf queue pause
loaf queue resume
loaf queue cancel QUEUE_REQUEST_ID
```

Pause prevents new dispatch, not accepted execution. Cancellation only succeeds
before dispatch and does not replace `loaf stop RUN_ID`. Restarting a Coordinator
recovers its saved requests. Automatic restart after reboot depends on an
explicitly configured, supported service manager; queue enablement never powers
on a stopped cloud instance.

## Delete selected canceled inputs

```bash
loaf queue clean QUEUE_REQUEST_ID --dry-run --json
loaf queue clean QUEUE_REQUEST_ID --yes --json
```

The result contains `dry_run`, `request_ids` and the queue `result`. Only selected,
safely canceled preparation inputs are eligible. Active Runs, shared datasets
and local downloads are excluded; request identity and no-start facts remain.
Ordinary `loaf clean RUN_ID` keeps its confirmed-terminal-Run contract.

Remote state survives local disconnection while the remote disk remains intact.
A powered-off machine cannot execute or dispatch. Destroying that disk removes
same-disk queue/event state; deliver or export required facts beforehand.
