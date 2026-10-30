---
name: loaf
description: Use Loaf to run recoverable experiments on existing SSH machines, inspect results and retrieve outputs. Also handle explicitly authorized AutoDL operations, optional FIFO queues, notifications, completed-task cleanup and failed-preparation abandonment. No implicit rentals or queue enablement.
---

# Run an experiment and keep its identity

Work in the intended project directory. Use an authorized existing machine;
changing the project selection requires the user's intent.

```bash
loaf machine list --json
loaf machine use lab --json
loaf run --wait 10s --json -- python3 train.py
```

Select a machine only when necessary. Register an existing connection with
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

If `next_actions` is present, preserve its `command` argv array, explicit IDs and
confirmation requirement. Do not split display text on spaces, insert `--yes`,
or infer authority from `requires_confirmation: false`. Put Loaf output options
before `--`. A historical Run/queue detail does not export its private command,
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
Never bypass host-key verification, replay an unconfirmed mutation, rent a
replacement automatically, or infer deletion authority from experiment completion.
