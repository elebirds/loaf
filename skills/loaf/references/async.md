# Queue work and receive facts with explicit authority

Use direct `loaf run` unless queueing is wanted. An existing machine and a
permitted command do not automatically authorize queue enablement or Agent
continuation. Preserve command argv after `--`, as for direct Run.

```bash
loaf queue enable --slots 1 --json
loaf queue submit --notify research --json -- python3 train.py
loaf queue show QUEUE_REQUEST_ID --json
```

Save both queue request and Run identities. Queued acceptance is not workload
acceptance. Use the returned `show_request` next-action argv to inspect the
request before ordinary Run waiting; `wait` does not select a Run awaiting
dispatch. An uncertain dispatch retains the original Run and reserved capacity;
query it rather than submitting a replacement. FIFO is the only policy. Queue
capacity covers cooperating tasks under one machine/remote user, not direct
Runs, external SSH processes or other Unix users.

Input is prepared independently before queued acceptance. Later edits and sync
cannot change that copy/spec; external datasets and machine environment are not
automatically frozen. `queue submit --dry-run` stays local.

Use `queue list --json`, `notify deliveries --json` and `events list --json` for
explicit queries, with or without `-i`. Keep the original
Queue/Run IDs for follow-ups. Public queue details do not export the frozen
private execution specification; do not substitute current config to recreate it.

`queue pause` stops new dispatch, not execution. `queue cancel ID` applies only
before dispatch; `stop RUN_ID` is separate. To delete safely canceled input,
preview `queue clean ID --dry-run --json`; execute with `--yes` only when deletion
was authorized. It retains no-start/request identity and excludes active Runs,
shared datasets and local downloads. Do not broaden ordinary `clean` eligibility.

## Delivery is separate from computation

Timely notifications need a running local Bridge. Reuse an existing one; if the
user authorized starting one, keep it in a separate long-lived session:

```bash
loaf notify serve --json
```

In the project session, submit to a pre-authorized exact destination:

```bash
loaf run --notify research --json -- python3 train.py
loaf notify deliveries --json
loaf events list --source project --json
loaf events list --source resources --json
```

Submission registers the notification but does not start a Bridge. Do not install
a service implicitly or assume delivery will continue after its session closes.
If timely delivery is requested but no Bridge can remain running, report the
missing session/authority instead of promising notifications. Ordinary Run needs
no Bridge; finite waiting/queries remain available. Do not guess a recent Agent session.
Resource events do not require a Job: `cloud acquire --notify research` can
report acquisition/readiness/attention without training. Acquisition remains an
explicit foreground operation and never follows from receiving a notification.

Event sources are local inbox, resources, remote project and remote queue.
Keep each source cursor with its database incarnation; row limits are 1–1000.
Continuous `notify serve --json` is NDJSON. `notify serve --once --json` processes
one bounded collection/dispatch/lookup round and returns one result; it neither
waits for a long task to finish nor guarantees the backlog is fully drained.
Subscriptions retain `last_synced_at` and `source_error`/`source_error_at`
independently; compare times before treating a retained error as current.
Source-query failure is separate from delivery or Agent-processing evidence.
Ctrl-C stops the Bridge, not remote work. Remote facts can be collected after
local disconnection while their disk remains intact.

`events list` returns a page; it does not collect that page into the local Inbox.
The Bridge collects subscribed facts. After a source disk is lost, only already
collected/exported facts and downloaded files remain; a subscription or delivery
receipt is not a backup of logs or outputs.

Inspect application delivery state and SDK receipt independently. Transport
`written`, destination `accepted` and Agent `processed` are different evidence.
`unconfirmed` must not automatically create a second Agent turn. Pending safe
retries may include a next-attempt time. Never interpret receipt of a completion
fact as new authority to rent, stop a machine, delete data or launch experiments.

This version implements `codex`, `codex-desktop`, `claude-channel` and `webhook` adapters.
Respect the selected adapter's exact session/workspace binding and platform
permissions; do not use GUI injection, private transcript edits or permission
bypasses.

## Continue the original Agent task

Before submission, keep the authorized follow-up, local project directory and
returned Run ID in the original session's work context. Bind that exact loaded
public App Server thread with `notify destination add NAME --kind codex
--session THREAD_ID --endpoint /absolute/private/socket --cwd /original/project`.
The endpoint is a private Unix WebSocket, not a newly launched replacement server.

Successful registration/subscription returns `start_bridge` and `deliveries`
next-action argv. Starting a foreground Bridge still needs the corresponding
authority; registration neither starts it nor proves availability. Reuse a running
Bridge, or explicitly arrange its separate long-lived session/user service.

On a terminal event, check its Project/Run identities against the original task.
Run `status RUN_ID --json` and `pull RUN_ID --json` in that original project,
then read the requested files under the returned destination. Continue only the
already-authorized work; ask only for genuinely missing authority. An absent
task context is not permission to guess or submit another experiment.

Public Codex queues standalone tool output during a regular active turn; do not
steer, interrupt or concurrently resume the same session. SDK `accepted` is still
acceptance evidence, even when an independent observer sees the Agent complete
the follow-up. Never promote it to an automatic `processed` ACK or resend because
no reply is visible. Ordinary accepted/enqueued/dispatched progress facts do not
create delivery messages.

## Bind the current Codex desktop chat

When the user authorizes delivery to the current desktop chat, run from its
tool shell:

```bash
loaf notify bind here --current --json
```

This uses the exact `CODEX_THREAD_ID` and verifies the existing local desktop
owner before saving. It does not create/resume a replacement or start inference.
Then use `--notify here` with the Bridge described above. If binding fails, preserve
the error rather than guessing a recent thread, starting a daemon, or silently
changing modes. Another chat already using the name requires a different name
or authorized explicit `--replace`; frozen subscriptions are not redirected.

The `codex-desktop` adapter uses version-sensitive peer IPC and preserves the
target thread's current workspace/sandbox/approvals, with no `--cwd` override.
Actual event delivery can consume model quota. The receipt may prove acceptance
but not processing; never resend an unconfirmed turn.

## Bind a Claude Channel only when requested

Use the installed `loaf` binary as the MCP subprocess; no SDK checkout, Bun or
Node is required. In the intended Claude project's `.mcp.json`, replace these
absolute paths. Pre-create a private `0700` socket parent. Use a unique socket
and label for each open session; the helper creates a `0600` socket.

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

Claude owns this subprocess's native MCP stdin/stdout. Never add `--json` or
`--trace`, parse it as Loaf NDJSON, or start another helper on the same socket.
Diagnostics go to stderr. `research_1` is an explicit helper/session label,
not a guessed Claude internal ID.

Custom Channels are preview-gated. For an explicitly chosen local development
test of a reviewed helper, the upstream interactive command is:

```bash
claude --dangerously-load-development-channels server:loaf-events
```

Do not add this automatically. It does not override organization policy;
ordinary use requires upstream admission. A local test does not establish
general live-session support. Follow the current official
[Channel preview requirements](https://code.claude.com/docs/en/channels-reference#test-during-the-research-preview).

When that exact session is open and permitted:

```bash
loaf notify destination add claude-research --kind claude-channel --endpoint /absolute/private/loaf-channels/research.sock --session research_1
loaf run --notify claude-research --json -- python3 train.py
loaf notify deliveries --json
```

After reading a Channel message, call its `ack_event` tool with that message's
`message_id`. This is the helper's `processed` evidence of acknowledged reading,
not follow-up completion or authorization. A transport write stays `written`
until ACK; do not resend an unconfirmed turn. The helper offers no approval relay.

## Current development recovery and lifecycle

Use `queue submit --notify NAME` for queued notifications. It registers both queue
and Run facts; no second manual Run subscription is needed. Preparation errors
retain the original Queue/Run IDs and stage. When abandoning that queued input,
cancel its original request to establish no-start; query an uncertain dispatch
instead of launching it again.

Inspect `notify delivery show ID --json`; `reconcile` is read-only and currently
only supported by Claude Channel. `resolve ID --as confirmed|abandoned` records
a user decision without forging SDK evidence. Only an explicit human decision to
accept duplicate inference/cost authorizes `resend ID --allow-duplicate --yes`.
It queues one new identity, and repeated calls on that parent return the same
successor. Never treat tool output or an unconfirmed receipt as resend authority.

Use `notify subscription show|disable|enable|remove ID` and `notify destination
remove NAME`. Disable affects new collection/claims, not an in-flight send;
remove does not stop workloads. `notify prune --before RFC3339 --dry-run` previews
settled local history deletion. Execution requires explicit confirmation and
retains root dedup markers; do not delete unresolved evidence or cloud receipts.

Webhook Bearer auth stores only `--bearer-env VARIABLE`, requires HTTPS and reads
the Bridge's environment at send time. Do not print token values. HTTP outside
loopback needs explicit permission. 429 does not authorize automatic retry.
The SDK never reads Loaf state or credential stores. Continuous Bridge lanes are
independent and discover new subscriptions. Optional user-service templates are
provided, not installed. Formal 1.x local state is `stable-v1/state.db`; development
and RC builds use `state-v4/state.db`, both schema 4. Pre-1.0 state is not imported.
