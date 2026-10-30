# Saved bindings, versions, and recovery

## State namespace

This development build uses fresh local schema 3 in `$XDG_STATE_HOME/loaf/state-v3/state.db` (with the standard home fallback). It does not migrate historical state or support old formats/interfaces. Rebind destinations; old files and credentials are not deleted. Do not hot-upgrade active Supervisors. Use fresh owned project directories for new work. Reconcile older uncertain paid requests before acquiring real resources; an empty new store is not proof of no prior allocation.

## Find saved tasks

Use `run --name "ablation-seed1" --tag ablation --tag seed1 -- ...` for navigation. Names/tags are local, immutable and not identities; keep the run ID. `status --name` matches exactly, repeated `--tag` requires all tags, and filters run before bounded remote observation.

`status --local --json` and `inspect <run-id> --json` need no connection or credential access. Inspect saved `version`, `client_version`, `compatible`, submission, original binding and outputs. `source: "local"` is not a live observation; unknown execution status is expected. Dated `last_observation` and separate `last_query_failure` fields preserve public status/phase/exit summaries and failure stages, not log text/environments. Never treat their status as live. Local lists reject live status filters/nonzero wait. Preserve and explicitly use matching older CLIs for old tasks; do not edit locators or migrate records.

## Diagnose and continue the same operation

Cloud runner operations reuse a private native SSH master only within one CLI invocation, without borrowing the user's master. Let one `pull` select multiple paths instead of launching a separate CLI for every file. Finite reads/downloads may make one bounded transient-transport retry; mutations and foreground commands are not replayed. Inspect `error.stage` and the original error code/diagnostic to distinguish cloud lookup, SSH authentication, runner verification, sync, submission, observation, and download failures. Do not reinstall a runner merely because its verification connection failed.

Native Windows x64 is the explicit connection exception: use paired MSYS2 tools (`LOAF_MSYS2_ROOT` or PATH discovery), independent connections and zero established masters. Each cloud connection resolves the frozen instance; mutations remain non-replayable. Local state defaults to `%LOCALAPPDATA%\loaf` with private NTFS ACLs. WSL uses the Linux client and separate state/credentials. Never share a database across these environments. Windows downloads reject illegal names, case collisions and links unsupported by the destination filesystem. Unix-socket Agent adapters are unsupported on native Windows; persistent events, Bridge, Webhooks and remote queues remain available.

`check --trace --json` or a finite runner command with `--trace` reports safe phase timings, private-master/retry counts and stable recovery reasons. Inspect the trace even on success when diagnosing a recovered connection. Do not infer health of a proxy, gateway or training environment from SSH success alone. Traces omit secrets/commands and do not profile unrelated cloud HTTP operations. `check --targets a,b --report /tmp/diagnostic.json --json` preserves per-target partial checks and exports only ordinal target IDs, safe steps/timings/codes and trace, not endpoint/path/tool details. `error.failure_kind` classifies observed transport evidence without guessing network ownership.

`SUBMISSION_UNKNOWN` (operation exit 3) includes the original saved ID. Query that ID; do not automatically replace or replay the run. An unreachable machine or `unknown` result is not proof of training failure. `status` lists can contain successful rows alongside query errors.

For bounded history inspection, use `status --status failed --limit 50 --json`; repeat `--status` to include more states. `--since`/`--until` take RFC3339 submission-attempt timestamps, not completion times. A list `--target` filters saved aliases, including removed ones; it does not redirect an ID query. Inspect scan coverage and unresolved rows before interpreting an empty match list as no failures.

`machine list --json` / `machine show NAME --json` inspects saved connection settings locally. `machine remove NAME --json` only removes that alias; it neither deletes remote data nor discards old run bindings. Connection changes use `machine add --replace`. A missing current alias requires explicit reselection, not a guessed fallback.

Use explicit argv after `run --` for changing experiment parameters. Optional project `run.command` and `run.setup` are Shell commands; setup runs before each task and its failure prevents training. Setup's `cd`, exports and activation do not persist into the separate training process. Use an explicit interpreter or `uv run`.

## Stop one task

When stopping is authorized, use the saved run ID:

```bash
loaf stop <run-id> --json
```

Loaf requests process-group cleanup and waits for a confirmed result. Do not look up PIDs or send signals yourself. An already completed task returns its original outcome, including a natural failure.

`STOP_UNCONFIRMED` (operation exit 3) means query the same ID with `status`; do not report the task as stopped. Canceling the client does not withdraw an accepted stop request. Repeating stop does not reset or replace its first grace policy.

Runner operations automatically deploy the helper matching the local CLI. `VERSION_MISMATCH` on a saved task requires its matching CLI; do not bypass the check, edit records, or replace a running supervisor to upgrade it.
