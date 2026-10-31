---
name: loaf
description: Use Loaf's CLI to run projects or foreground commands on authorized SSH machines, inspect status and logs, and retrieve outputs. Also handle explicitly authorized AutoDL, FIFO queue, notification and cleanup operations. Prefer existing targets; no implicit rentals or service enablement.
---

# Run an experiment and keep its identity

If Loaf is missing, follow the [installation guide](../../manual/en/installation.md)
when installation is authorized. The installers select the newest public release
(including RCs), overwrite their installed package and configure user PATH;
they do not retain older executables or migrate task state.

Work in the intended project directory. With a known authorized machine, submit
directly without changing the project's default selection:

```bash
loaf run --target lab --wait 10s --json -- python3 train.py
```

Use `machine list --json` only when the target is unknown; use `machine use NAME`
when the user wants to change the project default, not for every submission.
Register an existing connection with
`loaf machine add lab --ssh lab-gpu --json`; this saves local settings and does
not connect. `run` already checks prerequisites, deploys the matching helper,
synchronizes working source, and prepares an independent working copy.
Do not routinely wrap it in `check`, `sync`, PID checks, or a remote launcher.

Save every returned `data.run_id`, including on error. Task IDs are
project-scoped and remain bound to the original machine and location.
Use that explicit ID in later calls:

```bash
loaf status r_0123456789abcdef01234567 --json
loaf logs r_0123456789abcdef01234567 --tail 100 --json
loaf pull r_0123456789abcdef01234567 --json
```

Example IDs are placeholders. `pull` defaults to the output selection frozen at
submission and `loaf-results/<run-id>/` under the project. Missing outputs can
still leave useful downloaded selections; inspect the partial result.

## Decide from fields, not wording or the process exit alone

| Evidence | Action |
| --- | --- |
| `ok: true` | The CLI operation succeeded. Read task `status` and `exit_code` separately. A query can succeed while training failed. |
| `submission: "submitted"`, later observation fails | Acceptance is confirmed. Keep the ID and query it; do not submit a replacement. |
| `submission: "uncertain"` / `SUBMISSION_UNKNOWN` | Query the same ID. Nonzero CLI exit is not permission to run again. |
| `submission: "preparing"` | Inspect that explicit ID offline. It is not accepted training and is excluded from history/latest selection. |
| `ABANDON_UNCONFIRMED` | Retain `no_start_at` and `cleaned_at` independently; retry only the same authorized `abandon` ID. |
| `state_error` | Local saving failed. Retain any verified remote status/exit result; do not treat it as `query_error`. |
| `QUERY_PARTIAL`, `OUTPUT_MISSING`, or a batch error | Read successful rows and coverage as well as the error. Do not discard all `data`. |
| `status: "unknown"` or a dated `last_observation` | Current execution is unverified. Do not report failure or a cached state as live. |
| Wait ends or observation is canceled | Accepted work continues. Neither event is a stop request. |

`--wait 10s` is a bounded observation budget, not a remote runtime limit.
Use finite observations instead of a busy polling loop. Operation exits are
0 success, 2 input/configuration, 3 unconfirmed mutation, and 1 other failure.
Public `message`/`hint` are localized; branch on codes and fields.

Use complete commands with `--json`, not simulated menu input. Humans use
`cloud acquire -i` for GPU comparison and local prompts for login, registration
or confirmation. Do not add `-i` / `--interactive` to Agent calls. Ordinary
queries stay finite even with `-i`; bare root/cloud show offline help.
`--json` controls output format and cannot combine with `--interactive`.
Prefer explicit `machine list`, `queue list`, `notify deliveries` and
`events list` queries. Use `status --json` for live bounded queries or
`status --local --json` for saved facts. Names and task IDs remain explicit;
only guided machine registration fills missing inputs.
When an input error includes `issues`, correct the listed local omissions
together; keep the primary code and argument for control flow. The list is not
evidence that SSH or platform validation ran.

## First SSH trust

Trust the configured provider to resolve the authorized instance's endpoint.
Provider-backed SSH uses `accept-new`: the original command, including JSON,
accepts and records a new host key without another SSH call or fingerprint prompt.
Do not add a separate trust handshake or approval step.

Existing keys must still match, and explicit per-target SSH trust settings take
precedence. A changed key requires investigation, not automatic removal or disabled
checking. Static SSH targets keep their existing configuration; when an authorized
native session presents a first-use prompt, answer `yes` for the intended server.

## Preserve argument boundaries

Put CLI options before `--`. Arguments after `run --` and `exec --` are literal
argv, including spaces, commas, empty strings, and program options named
`--json` or `--lang`. Shell syntax requires an explicit Shell invocation.
Configured `run.command` and `run.setup` are Shell commands; setup activation,
exports and `cd` do not persist into the separate training process.

```bash
loaf run --dry-run --json -- python3 train.py --label 'seed 1'
loaf help run --json
```

The run preview is entirely local: no SSH, credential access, IDs, or state
writes. It exposes environment names, not values. `help --json` returns argument
and flag metadata in addition to human help; retain explicit false/zero/empty
values when valid instead of dropping them during argument construction.

If `next_actions` is present, use its `command` argv array without splitting display
text on spaces or changing identities. The suggestion and
`requires_confirmation: false` do not grant authority. Add `--yes` only when the
user has already authorized that operation and scope and the command supports it;
otherwise obtain the missing decision. Put Loaf output options before `--`.
A historical Run/queue detail does not export its private command,
setup or environment; a new submission needs explicitly supplied inputs or the
intended current project configuration, not a reconstructed “run again” action.

Use `exec` only for authorized foreground machine work, not as a shortcut for
submitting local project code. It has no sync, setup, run ID, or durability.

## Read the reference for the operation you actually need

- [Cloud](references/cloud.md): login, bind, acquire, clone, power and request
  reconciliation. Read before cloud operations; compute acquisition and whole-
  instance shutdown need their own authority.
- [Streams](references/streams.md): foreground `exec`, NDJSON, log cursors,
  acknowledged resume files, reconnection and output-consumer failures.
- [Outputs and cleanup](references/outputs-and-cleanup.md): frozen-group waiting,
  partial/batch downloads, failed-preparation abandonment and authorized terminal-data deletion.
- [Recovery](references/recovery.md): offline inspection, labels, saved bindings,
  diagnostics, old state namespaces, matching CLI versions and stopping a task.
- [Queues and notifications](references/async.md): explicit FIFO admission,
  durable resource/Run facts, exact destinations and delivery evidence.

Do not read credentials or internal state files to replace public commands.
Do not automatically repeat a possibly accepted mutation or rent a replacement.
Observe/reconcile the original identity first. Explicit retries of the same
authorized `clean`/`abandon` IDs follow their recovery contracts, not a new task.
Experiment completion does not grant deletion authority.
