---
name: loaf
description: Run and stop local project experiments on existing SSH machines with Loaf, inspect status and logs, and collect outputs across sessions. Use for Loaf-managed execution, not machine rental or resource scheduling.
---

# Run experiments with Loaf

Work from the project directory and use an authorized existing machine. Register or select it with `loaf add` / `loaf use` when needed; do not change an existing selection without the user's intent.

Submit the intended experiment command, without writing a remote launcher:

```bash
loaf run --wait 10s --json -- python3 smoke.py
```

Loaf handles prerequisites, source synchronization, independent work copies and background supervision. Do not add a routine sequence of `check`, `sync`, PID checks or internal-file inspection around `run`. Use `check` or the local `sync --dry-run` when their diagnostic information is needed.

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

Use explicit argv after `run --` for changing experiment parameters. Optional project `run.command` and `run.setup` are Shell commands; setup runs before each task and its failure prevents training. Setup's `cd`, exports and activation do not persist into the separate training process. Use an explicit interpreter or `uv run`.

## Stop the intended run

When stopping is authorized, use the saved run ID:

```bash
loaf stop <run-id> --json
```

Loaf requests process-group cleanup and waits for a confirmed result. Do not look up PIDs or send signals yourself. An already completed task returns its original outcome, including a natural failure.

`STOP_UNCONFIRMED` (operation exit 3) means query the same ID with `ps`; do not report the task as stopped. Canceling the client does not withdraw an accepted stop request. Repeating stop does not reset or replace its first grace policy. `STOP_OPTIONS_UNSUPPORTED` (exit 2) means the saved older runner cannot honor the configured grace; report that incompatibility rather than silently discarding the policy.

## Collect the outputs

```bash
loaf pull <run-id> --json
```

The default uses output paths saved at submission and downloads to `loaf-results/<run-id>/` under the project. Changing the current target or project outputs does not redirect that task. Repeat `--path` to override the selection for one download; `--to` is the exact destination, relative to the current directory when not absolute.

`OUTPUT_MISSING` (exit 1) can still include useful files: inspect `data.downloaded_paths` and `data.missing_paths`. A failed transfer does not claim a completed `changed_files` count. Retry the same pull after a download error; do not restart the experiment to retrieve files. Downloads retain local extras, and canceling pull does not stop training.

Running tasks may be pulled, but the result is a live copy, not a consistent checkpoint snapshot. Choose completion or atomically published checkpoint files when needed. `--checksum` detects same-size, same-time content changes with extra disk reads; it is not normally required.

Choose data, dependencies, smoke tests and scientific acceptance for the actual experiment. Loaf does not assess metrics or automatically repair environments. Public `exec`, interactive SSH, port forwarding, scheduling and provisioning are not available in this version.
