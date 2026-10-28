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
acceptance. An uncertain dispatch retains the original Run and reserved capacity;
query it rather than submitting a replacement. FIFO is the only policy. Queue
capacity covers cooperating tasks under one machine/remote user, not direct
Runs, external SSH processes or other Unix users.

Input is prepared independently before queued acceptance. Later edits and sync
cannot change that copy/spec; external datasets and machine environment are not
automatically frozen. `queue submit --dry-run` stays local.

`queue pause` stops new dispatch, not execution. `queue cancel ID` applies only
before dispatch; `stop RUN_ID` is separate. To delete safely canceled input,
preview `queue clean ID --dry-run --json`; execute with `--yes` only when deletion
was authorized. It retains no-start/request identity and excludes active Runs,
shared datasets and local downloads. Do not broaden ordinary `clean` eligibility.

## Delivery is separate from computation

```bash
loaf run --notify research --json -- python3 train.py
loaf notify serve --once --json
loaf notify deliveries --json
loaf events list --source project --json
loaf events list --source resources --json
```

Use a pre-authorized exact destination; do not guess a recent Agent session.
Resource events do not require a Job: `cloud acquire --notify research` can
report acquisition/readiness/attention without training. Acquisition remains an
explicit foreground operation and never follows from receiving a notification.

Event sources are local inbox, resources, remote project and remote queue.
Keep each source cursor with its database incarnation; row limits are 1–1000.
Continuous `notify serve --json` is NDJSON, while `--once` returns one result.
Ctrl-C stops the Bridge, not remote work. Remote facts can be collected after
local disconnection while their disk remains intact.

Inspect application delivery state and SDK receipt independently. Transport
`written`, destination `accepted` and Agent `processed` are different evidence.
`unconfirmed` must not automatically create a second Agent turn. Pending safe
retries may include a next-attempt time. Never interpret receipt of a completion
fact as new authority to rent, stop a machine, delete data or launch experiments.

Existing-session, managed-session and explicit headless/resume integrations have
different capability guarantees. Respect the installed adapter and platform
permissions; do not use GUI injection, private transcript edits or permission
bypasses when a mode is unavailable.
