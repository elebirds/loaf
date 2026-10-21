---
name: loaf
description: Use Loaf to inspect existing SSH machines, run and stop project experiments, collect outputs, and explicitly clean completed task data. Use for Loaf-managed research workflows, not machine rental or resource scheduling.
---

# Run experiments with Loaf

Work from the project directory and use an authorized existing machine. Register or select it with `loaf add` / `loaf use` when needed; do not change an existing selection without the user's intent.

Submit the intended experiment command, without writing a remote launcher:

```bash
loaf run --wait 10s --json -- python3 smoke.py
```

Loaf handles prerequisites, source synchronization, independent work copies and background supervision. Do not add a routine sequence of `check`, `sync`, PID checks or internal-file inspection around `run`. Use `check` or the local `sync --dry-run` when their diagnostic information is needed.

## Choose foreground inspection or a durable experiment

```bash
loaf exec --json -- nvidia-smi
```

Use `exec` for an authorized foreground machine operation, not as a faster submission path for local project code. It streams bounded NDJSON output and returns the remote exit result, with no source sync, setup, saved run ID, or durable execution guarantee. Its default directory is the remote user's home; `--cwd` overrides it. Preserve argv after `--`; explicitly invoke a Shell for Shell syntax.

`EXEC_UNCONFIRMED` (exit 3) means execution may have happened. Do not automatically replay a mutating command after cancellation or connection loss. A confirmed nonzero remote exit is `EXEC_FAILED` (exit 1), not a transport failure. Choose `run --wait` when even a short experiment needs synchronized sources and a recoverable result.

`ssh` opens a native human terminal. `forward <remote-port> --local-port <local-port>` keeps a dedicated loopback SSH tunnel in the foreground; it does not start or check an application. Neither accepts JSON. Closing the forward removes that tunnel, not the remote service or another SSH master.

## Keep the ID; inspect the result

- Save `data.run_id`. IDs are project-scoped and stay bound to their submitted machine and location.
- `ok` describes the CLI operation. Inspect `data.status` and `data.exit_code` for the experiment result; CLI exit 0 does not mean training succeeded.
- `--wait 10s` is an observation budget, not a runtime limit. If still running, query the same ID later. Use `--wait 0s` for immediate return after acceptance.

```bash
loaf ps <run-id> --wait 30s --json
loaf logs <run-id> --tail 100 --json
```

Prefer finite queries over a busy polling loop. Log tails are line- and byte-bounded; `log.truncated` means earlier output was omitted. For explicitly requested live observation, `loaf logs <run-id> -f --json` emits NDJSON chunks and an end/error result. Canceling observation does not stop accepted work.

## Handle uncertainty without duplicate experiments

`SUBMISSION_UNKNOWN` (operation exit 3) includes the original saved ID. Query that ID; do not automatically replace or replay the run. An unreachable machine or `unknown` result is not proof of training failure. `ps` lists can contain successful rows alongside query errors.

For bounded history inspection, use `ps --status failed --limit 50 --json`; repeat `--status` to include more states. `--since`/`--until` take RFC3339 submission-attempt timestamps, not completion times. A list `--target` filters saved aliases, including removed ones; it does not redirect an ID query. Inspect scan coverage and unresolved rows before interpreting an empty match list as no failures.

`targets [name] --json` inspects saved connection settings locally. `remove <name> --json` only removes that alias; it neither deletes remote data nor discards old run bindings. Connection changes use `add --replace`. A missing current alias requires explicit reselection, not a guessed fallback.

Use explicit argv after `run --` for changing experiment parameters. Optional project `run.command` and `run.setup` are Shell commands; setup runs before each task and its failure prevents training. Setup's `cd`, exports and activation do not persist into the separate training process. Use an explicit interpreter or `uv run`.

## Stop the intended run

When stopping is authorized, use the saved run ID:

```bash
loaf stop <run-id> --json
```

Loaf requests process-group cleanup and waits for a confirmed result. Do not look up PIDs or send signals yourself. An already completed task returns its original outcome, including a natural failure.

`STOP_UNCONFIRMED` (operation exit 3) means query the same ID with `ps`; do not report the task as stopped. Canceling the client does not withdraw an accepted stop request. Repeating stop does not reset or replace its first grace policy.

Runner operations automatically deploy the helper matching the local CLI. `VERSION_MISMATCH` on a saved task requires its matching CLI; do not bypass the check, edit records, or replace a running supervisor to upgrade it.

## Collect the outputs

```bash
loaf pull <run-id> --json
```

The default uses output paths saved at submission and downloads to `loaf-results/<run-id>/` under the project. Changing the current target or project outputs does not redirect that task. Repeat `--path` to override the selection for one download; `--to` is the exact destination, relative to the current directory when not absolute.

`OUTPUT_MISSING` (exit 1) can still include useful files: inspect `data.downloaded_paths` and `data.missing_paths`. A failed transfer does not claim a completed `changed_files` count. Retry the same pull after a download error; do not restart the experiment to retrieve files. Downloads retain local extras, and canceling pull does not stop training.

Running tasks may be pulled, but the result is a live copy, not a consistent checkpoint snapshot. Choose completion or atomically published checkpoint files when needed. `--checksum` detects same-size, same-time content changes with extra disk reads; it is not normally required.

## Clean only authorized completed task data

```bash
loaf clean <run-id> --dry-run --json
loaf clean <run-id> --yes --json
```

Cleanup requires explicit IDs and authority to remove their remote working files, logs, and outputs. Finishing an experiment does not itself authorize deletion. Use the read-only preview to choose scope when needed; `--yes` supplies the noninteractive confirmation once deletion is authorized. A preview does not reserve eligibility: actual deletion rechecks the run.

Local downloads, other tasks, source caches and shared environments remain. Small terminal/ID records are retained to prevent duplicate execution. `ps` preserves the original result with cleanup metadata; `RUN_CLEANED` from `logs`, `pull` or `stop` is expected, not a reason to select an older run or repeat training. For a partial or unconfirmed cleanup, inspect per-ID results and retry only those same explicitly authorized IDs. Do not stop active tasks implicitly to make them eligible.

Choose data, dependencies, smoke tests and scientific acceptance for the actual experiment. Loaf does not assess metrics, automatically repair environments, schedule resources or provision machines.
