---
name: loaf
description: Use Loaf to inspect SSH machines, explicitly acquire or connect AutoDL compute when authorized, run and stop experiments, collect outputs, and explicitly clean completed task data. No implicit rentals or workload scheduling.
---

# Run experiments with Loaf

Work from the project directory and use an authorized existing machine. Register or select it with `loaf add` / `loaf use` when needed; do not change an existing selection without the user's intent.

## Acquire compute only when authorized

Use existing SSH targets without cloud configuration. When the user authorizes cloud operations, choose the configured account and product explicitly. The adapters support a verified main/sub session for ordinary AutoDL and Pro, plus a separate developer-token Pro adapter; unsupported combinations do not switch control planes. An account's default product is only a preference. `--product` overrides profile `product`, then that default, but cannot redirect an already bound alias. A human can use `cloud login <name>` with hidden input, choosing `--credential-store keyring` or explicit unencrypted `file`; new-login defaults belong in user `[cloud] credential_store`, not project TOML. Login remembers a replayable password digest unless disabled. Treat it as a password; never request it in chat. Use configured references, not browser-token extraction, credential files, printed tokens or training environments. `cloud login <name>` explicitly reauthenticates with a saved digest; do not automatically repeat password failures or bypass verification. Logout deletes only the selected credential entry.

```bash
loaf cloud search --account autodl-main --profile train --json
loaf cloud acquire gpu-a --account autodl-main --profile train --wait 30m --yes --json
loaf use gpu-a --json
loaf run --wait 10s --json -- python3 smoke.py
```

`search` is one read-only query; `watch --wait 10m --json` monitors read-only in the foreground with NDJSON. Inspect hard requirements, unknown fields, observation time and pagination coverage; “no match” in a bounded result is not proof of no platform stock. Profiles are optional; direct GPU/memory/region/image/price flags override project/user defaults. GPU memory is per card; an hourly cap covers whole-instance compute, not storage or total spend. The user's separate safety ceiling cannot be raised by project/CLI overrides.

`acquire --yes` authorizes at most one paid instance, not training, resource release or automatic shutdown. Inventory and prices can race allocation. Session-backed Pro has catalog/quote search and monitoring; numeric `--gpu-spec-id` differs from developer `--gpu-spec` UUID. Unknown GPU memory/vendor stays ineligible when required; aggregate stock is not a spec reservation. The developer-token Pro adapter has no verified read-only stock/prequote capability: use an explicit spec/image/CUDA and `--allow-unquoted` only with specific user approval for unquoted allocation attempts. It cannot bypass a configured price cap or unverified hardware floors. Do not treat Elastic stock as Pro stock.

### Clone an existing environment and data

For an authorized same-region migration, use the native acquisition command rather than a temporary API wrapper:

```bash
loaf cloud acquire gpu-copy --account autodl-main --product standard \
  --clone-from old-gpu --max-compute-hourly-price 3.00 --wait 24h --yes --json
```

The source is a stopped ordinary instance ID or cloud alias. Loaf waits for stock only in its region and copies both disks; it never falls back to blank creation. Source image and default disk expansion are inherited. Omit image/spec/CUDA/system-disk flags; conflicting regions or smaller disks are errors. Clone waiting defaults to 30m unless configured; explicit zero remains one attempt. `ready` additionally requires successful data copying, not just SSH. Use `cloud info` for `clone_data_status`; on timeout or `CLONE_FAILED`, keep the receipt and resume/recover that same instance with `cloud bind --request`, not another acquisition.

Save `request_id` and the returned instance ID, including on failure. `CLOUD_UNCONFIRMED` (exit 3) means a resource may already exist or billing may have started. Inspect `cloud requests`, check the platform and `cloud reconcile <request-id> --instance <known-id>`; never rent a replacement automatically. `--no-instance --yes` clears duplicate protection only after explicit human confirmation that no resource was created, not an inference from an incomplete list.

`cloud bind gpu-a --request <request-id>` retries connection to the same created resource without renting. `ready` means platform running, authenticated SSH and saved target, not a ready ML environment. For an existing resource use `cloud bind gpu-a --instance <id> --account <name>`. Noninteractive first-host trust failures need a human/native SSH decision; do not bypass verification. Passwords are fetched transiently for native SSH; default `cloud credentials` redacts them. Do not request `--show-password` merely to run an experiment.

`cloud start/stop <id-or-alias> --yes --wait 2m --json` affect the whole instance, potentially other users' work. They require separate authority from `loaf stop`, which stops one experiment. Acceptance is not completion and storage may keep billing after power-off. For an unconfirmed power operation reconcile the same saved request/instance rather than replaying it. Removing/rebinding aliases does not redirect previously submitted experiments.

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
- `data.submission: "submitted"` preserves confirmed acceptance even if later observation fails with `status: "unknown"`. `"uncertain"` requires querying the same ID; neither observation failure nor a nonzero CLI exit authorizes another submission.
- `ok` describes the CLI operation. Inspect `data.status` and `data.exit_code` for the experiment result; CLI exit 0 does not mean training succeeded.
- `--wait 10s` is an observation budget, not a runtime limit. If still running, query the same ID later. Use `--wait 0s` for immediate return after acceptance.

```bash
loaf ps <run-id> --wait 30s --json
loaf logs <run-id> --tail 100 --json
```

Prefer finite queries over a busy polling loop. Log tails are line- and byte-bounded; `log.truncated` means earlier output was omitted. For explicitly requested live observation, `loaf logs <run-id> -f --json` emits NDJSON chunks and an end/error result. Canceling observation does not stop accepted work.

For explicitly requested continuous logs, `logs <run-id> -f --reconnect --json` allows one cursor-safe transient reconnection. Save `data.run.log.cursor` after consuming a chunk, or `data.log.cursor` from a finite query. Use `logs <run-id> -f --resume <cursor> --json` to continue manually; do not combine resume with tail. A changed/replaced log returns `LOG_CHANGED`; obtaining a fresh tail is an explicit reset, not automatic gap recovery. Default following remains single-connection. Cancellation or a failed output consumer never restarts a stream.

## Find and inspect saved tasks offline

Use `run --name "ablation-seed1" --tag ablation --tag seed1 -- ...` for navigation. Names/tags are local, immutable and not identities; keep the run ID. `ps --name` matches exactly, repeated `--tag` requires all tags, and filters run before bounded remote observation.

`ps --local --json` and `inspect <run-id> --json` need no connection or credential access. Inspect saved `version`, `client_version`, `compatible`, submission, original binding and outputs. `source: "local"` is not a live observation; unknown execution status is expected. Local lists reject live status filters/nonzero wait. Preserve and explicitly use matching older CLIs for old tasks; do not edit locators or migrate records.

## Handle uncertainty without duplicate experiments

Cloud runner operations reuse a private native SSH master only within one CLI invocation, without borrowing the user's master. Let one `pull` select multiple paths instead of launching a separate CLI for every file. Finite reads/downloads may make one bounded transient-transport retry; mutations and foreground commands are not replayed. Inspect `error.stage` and the original error code/diagnostic to distinguish cloud lookup, SSH authentication, runner verification, sync, submission, observation, and download failures. Do not reinstall a runner merely because its verification connection failed.

`check --trace --json` or a finite runner command with `--trace` reports safe phase timings, private-master/retry counts and stable recovery reasons. Inspect the trace even on success when diagnosing a recovered connection. Do not infer health of a proxy, gateway or training environment from SSH success alone. Traces omit secrets/commands and do not profile unrelated cloud HTTP operations.

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

Choose data, dependencies, smoke tests and scientific acceptance for the actual experiment. Loaf does not assess metrics, automatically repair environments or schedule workloads. Neither a request to run an experiment nor permission to edit code authorizes paid acquisition or whole-instance shutdown.
