---
name: loaf
description: Run local project experiments on existing SSH machines with Loaf, and retrieve their status and logs across sessions. Use for Loaf-managed execution, not machine rental or resource scheduling.
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

Choose data, dependencies, smoke tests and scientific acceptance for the actual experiment. Loaf does not assess metrics or automatically repair environments. Public `stop`, `pull`, `exec`, scheduling and provisioning are not available in this version.
