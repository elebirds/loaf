# Get results back and share a machine

[Home](../../README.md) · [Guide](guide.md) · [Commands](commands.md) · [简体中文](../zh-Hans/async.md)

Use a notification to deliver a saved result after a task finishes. Use a queue
when cooperating projects share a machine. A direct Run or cloud operation can
notify you without ever entering a queue.

For scripts and Agents, use explicit queries such as `loaf queue list --json`,
`loaf notify deliveries --json` and `loaf events list --json`. These calls return finite queries, with or without `-i`. Use
`loaf help queue submit --json` to inspect the submission arguments without
connecting. Read-only lists have no `--dry-run`; previews belong to operations
such as `queue submit`, `queue clean` and `notify prune`.

Native Windows supports persistent events, the foreground Bridge, Webhooks and
remote queues. The `codex`, `codex-desktop` and `claude-channel` Unix-socket
adapters return `DESTINATION_UNSUPPORTED` there. Their examples below apply to
verified Unix/WSL environments; WSL does not imply access to a Windows desktop
session.

## Let the original Codex session continue after a Run

In the intended session, first agree on the follow-up: for example, submit this
project, then query the same Run, download its outputs and explain the result.
Keep the local project directory and returned Run ID in that session's work
context. A completion event supplies facts, not a new task or permission.

For a public App Server that the user already runs, bind its exact loaded thread
and private Unix WebSocket endpoint. `--cwd` verifies the original workspace;
it does not move or resume the thread:

```bash
loaf notify destination add research --kind codex --session THREAD_ID --endpoint /absolute/private/app-server.sock --cwd /absolute/project
```

Keep one Bridge running in a separate local terminal, or explicitly install an
existing [user-service template](../../examples/notify-services/README.md):

```bash
loaf notify serve --json
```

From the original project, submit to an authorized machine:

```bash
loaf run --target lab --notify research --json -- python3 train.py
```

Destination binding and subscription results include `next_actions` for starting
a Bridge and querying deliveries. They do not start it or prove that one is
running. Reuse an existing Bridge instead of starting another owner.

After the terminal fact arrives, the Agent uses the original project and exact
Run ID with `status` and `pull`, reads the returned download location and
continues the work already authorized in that session. The summary includes
source/project and related operation identities so it need not guess from the
current Target. Codex queues standalone tool output when a regular turn is
active; Loaf does not steer or interrupt it. `run.accepted`, `queue.enqueued` and
`queue.dispatched` remain queryable facts without ordinary progress notifications.

The workflow is complete when that original Agent has handled the result.
The Codex adapter still reports `accepted` with its turn identity, not an
automatic `processed` acknowledgment. A missing response does not authorize a
second turn; inspect the delivery before an explicit recovery decision.

## Send notifications back to the current Codex desktop chat

Ask the Agent in the intended Codex desktop chat to run once:

```bash
loaf notify bind here --current
```

Loaf reads that tool shell's exact `CODEX_THREAD_ID`, discovers its existing
owner through local private desktop IPC, and saves the destination only after
verification. No socket or thread ID needs copying. It does not start another
App Server, create a thread, or resume a substitute. A normal terminal without
current-session context fails explicitly.

Later operations only need its name:

```bash
loaf run --notify here -- python3 train.py
loaf cloud acquire gpu-a --profile train --notify here
```

Keep `loaf notify serve` running in another local terminal, or collect later
with `--once`. Binding is read-only Agent discovery; actual delivery may cause
Agent processing and consume service quota. Rebinding the same name to the same
chat is idempotent. A name owned by another chat needs a new name or explicit
`--replace`; existing subscriptions keep their frozen destination.

The saved mode is `codex-desktop`, using experimental desktop peer IPC rather
than the public App Server WebSocket. The exact local thread must still have a
desktop owner that supports untrusted application input. Incompatible protocols
fail instead of selecting a recent chat. The thread retains its own workspace,
sandbox and approvals; this mode has no `--cwd` override.

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

This version implements four adapters: `codex`, `codex-desktop`,
`claude-channel` and `webhook`.

Use an endpoint supported by the selected adapter. A saved binding does not
prove reachability or processing capability. Loaf does not choose the latest
session, silently create a replacement, or bypass its workspace and approvals.
Claude Channel delivery requires an open session and platform permission.

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

Explicit false establishes the existing bounded source snapshot; it can read
the source and is not an offline setting. A Run subscription follows its saved
project binding. Do not attach the current target to a historical Run or infer
non-delivery from an empty page.

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
does not prove a workload started. Submission returns a `show_request`
next-action argv for `queue show REQUEST_ID`. Inspect that request before using
ordinary `wait`, which does not select a Run still awaiting dispatch.
Find the Run with `queue show`, then use its explicit ID with `status`, `logs`,
`stop` and `pull`. Uncertain dispatch is
reconciled against the original Run, not replaced by a second execution.

Queue details expose identity and state, not the private frozen command or
environment. To submit new work, supply its inputs explicitly; do not treat the
current project command as a reconstruction of an older request.

The shared list may include another project's requests. Show, cancel, clean and
Run actions require the original project's saved binding; open that project
rather than overriding the target. An ID in the list alone does not grant a new
project control of that request.

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

## Keep facts and files before destroying a remote disk

Formal 1.x releases use local `stable-v1/state.db`; development and RC builds
use separate `state-v4/state.db`. Both use the current schema-4 format. Formal
releases do not import pre-1.0 state; see [Upgrading](upgrading.md).

| Data | Saved location | After the remote disk is destroyed |
| --- | --- | --- |
| Run facts | Project `.loaf-control-v2/state.sqlite3` | Only previously collected or explicitly exported facts remain locally. |
| Queue requests and facts | SSH user's `~/.local/share/loaf/queue-v1/state.sqlite3` | The same-disk queue cannot be recovered from a subscription alone. |
| Work copy, logs and output files | Project `runs/RUN_ID/` | Only files already downloaded or otherwise saved remain. |
| Cloud operation receipts and resource facts | Local state database | Retained locally, independently of the instance disk. |
| Received Inbox, deliveries and source cursors | Local state database | Retained locally until explicit eligible pruning; delivery evidence does not back up output files. |

Before destroying a disk through the provider, finish or explicitly stop the
affected work and download the outputs you need:

```bash
loaf pull RUN_ID
loaf events list --source project --subject run:RUN_ID --limit 1000 --json > run-events-page-1.json
```

Check the downloaded files. For facts, inspect the returned `has_more`; when it
is true, read the next page with `--cursor` set to `next_cursor`, saving each page
separately. A cursor covers one source, not a cross-source snapshot. Save any
required logs separately; an event carries a public summary, not log contents.

Listing remote events returns a page but does not collect it into the Inbox.
The Bridge collects facts matching saved subscriptions. `notify serve --once`
handles a bounded batch, not necessarily the entire backlog; `last_synced_at`
does not establish that all remote history was copied. After source loss,
`events list --source local` can still read received facts and deliveries retain
their observed evidence. Source-query failures remain visible in subscriptions;
they neither recreate missing facts nor authorize another delivery.

## One queue intent, two independent streams

`queue submit --notify results` atomically registers one subscription containing
both the machine queue and the frozen Run's project source before source transfer.
Each binding has its own cursor and diagnostics in `sources`. A failure to create
either binding rolls back the whole registration. Preparation failures retain the
original Queue/Run IDs, `stage` and `next_action`; uncertain dispatch is observed,
never launched again. Canceling the original request before its first remote
prepare also records a durable no-start tombstone against delayed work.

## Recover one delivery

```bash
loaf notify delivery show DELIVERY_ID --json
loaf notify delivery reconcile DELIVERY_ID --json
loaf notify delivery resolve DELIVERY_ID --as confirmed --json
loaf notify delivery resolve DELIVERY_ID --as abandoned --json
```

Show includes frozen content, a safe recipient view, attempts, SDK evidence and
user resolution. Reconcile is read-only; only Claude Channel currently supports
Lookup. User confirmation never changes an SDK receipt into `processed`.

If you explicitly accept possible duplication, another Agent turn and service
cost, authorize one successor:

```bash
loaf notify delivery resend DELIVERY_ID --allow-duplicate --yes --json
```

It queues a new message identity with the same frozen fact, text and recipient;
the Bridge sends it later. Repeating this command for the same parent returns
the same successor. Interactive use without `--yes` asks for confirmation;
noninteractive use requires both flags. There is no bulk resend or automatic
resend of an unconfirmed message.

## Manage subscriptions and retained history

```bash
loaf notify subscription show SUBSCRIPTION_ID --json
loaf notify subscription disable SUBSCRIPTION_ID --json
loaf notify subscription enable SUBSCRIPTION_ID --json
loaf notify subscription remove SUBSCRIPTION_ID --json
loaf notify destination remove results --json
loaf notify prune --before 2026-10-01T00:00:00Z --dry-run --json
loaf notify prune --before 2026-10-01T00:00:00Z --yes --json
```

Disable stops new collection and claims; in-flight sends cannot be recalled and
are counted in the response. Enable continues saved cursors. Remove archives the
intent and closes unsent work, without stopping computation or erasing ambiguous
sends. Destination removal refuses references from subscriptions not yet removed.

Pruning is explicit, never automatic. It deletes settled history and unreferenced
old inbox payloads before the selected time, retaining compact deduplication
markers. It never deletes pending, in-flight, unresolved unconfirmed work, cloud
receipts, Run locators or remote events. Re-reading retained facts cannot recreate
already pruned root deliveries.

## Authenticate Webhooks and keep the Bridge running

```bash
loaf notify destination add results --kind webhook --endpoint https://receiver.example/loaf --bearer-env LOAF_HOOK_TOKEN
```

Supply the named variable to the Bridge using your existing secret-management
mechanism. Only its name is persisted; token values are not stored or accepted
through argv/project configuration. Missing credentials fail before HTTP sends.
Bearer requires HTTPS. Non-loopback HTTP requires `--allow-insecure-http`;
429 is not treated as proof of safe replay. Redirects remain disabled.

A continuous Bridge stays alive with no subscriptions and discovers newly created
ones. Collection, sending and ACK lookup have independent bounded lanes; slow
sources cannot block already-received deliveries. `--once` handles a bounded
batch, not necessarily the whole backlog. Its summary includes `rejected`;
confirmed rejection exits 1, and unconfirmed delivery exits 3.

Optional [launchd/systemd user-service templates](../../examples/notify-services/README.md)
are included in archives. Review paths and install them explicitly; Loaf does not
install services itself. Restarts recover persistent state. Sleep still prevents
local delivery, and facts cannot be recovered after their source disk is destroyed.

Subscription listing is ID-paginated, with 100 rows by default. JSON returns
`subscriptions`, `has_more` and `next_after`; continue with
`loaf notify subscriptions --after SUBSCRIPTION_ID --limit 100 --json`.
For multi-source intents, compare success/failure times within each `sources`
entry, never across different streams.

`rejected` counts last observed rejections on enabled intents, including items
waiting for a proven-safe retry; it can overlap `pending`. Missing Bearer
credentials therefore make a one-round command fail without an HTTP request.
Environment lookup is explicitly supplied by the CLI entry point.
