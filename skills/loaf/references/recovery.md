# Saved bindings, versions, and recovery

## Find saved tasks

Use `run --name "ablation-seed1" --tag ablation --tag seed1 -- ...` for navigation. Names/tags are local, immutable and not identities; keep the run ID. `status --name` matches exactly, repeated `--tag` requires all tags, and filters run before bounded remote observation.

`status --local --json` and `inspect <run-id> --json` need no connection or credential access. Inspect saved `version`, `client_version`, `compatible`, submission, original binding and outputs. `source: "local"` is not a live observation; unknown execution status is expected. Dated `last_observation` and separate `last_query_failure` fields preserve public status/phase/exit summaries and failure stages, not log text/environments. Never treat their status as live. Local lists reject live status filters/nonzero wait. A newer formal `1.x` CLI can manage earlier formal `1.x` work without changing its origin version or binding. Development/RC work needs its original tools and is not imported into formal history.

## Diagnose and continue the same operation

For local input failures, use `error.argument`, `reason` and the optional
`issues[]`; follow `help_command` as argv to inspect the real syntax. Correct
known local issues together, without reading credentials or querying a platform
to fill them. Help/version remain offline despite unrelated config failures.
Read-only lists have no `--dry-run`; use only an operation's existing preview.

Cloud runner operations reuse a private native SSH master only within one CLI invocation, without borrowing the user's master. Let one `pull` select multiple paths instead of launching a separate CLI for every file. Finite reads/downloads may make one bounded transient-transport retry; mutations and foreground commands are not replayed. Inspect `error.stage` and the original error code/diagnostic to distinguish cloud lookup, SSH authentication, runner verification, sync, submission, observation, and download failures. Do not reinstall a runner merely because its verification connection failed.

Native Windows x64 is the explicit connection exception: use paired MSYS2 tools (`LOAF_MSYS2_ROOT` or PATH discovery), independent connections and zero established masters. Each cloud connection resolves the frozen instance; mutations remain non-replayable. Local state defaults to `%LOCALAPPDATA%\loaf` with private NTFS ACLs. WSL uses the Linux client and separate state/credentials. Never share a database across these environments. Windows downloads reject illegal names, case collisions and links unsupported by the destination filesystem. Unix-socket Agent adapters are unsupported on native Windows; persistent events, Bridge, Webhooks and remote queues remain available.

`check --trace --json` or a finite runner command with `--trace` reports safe phase timings, private-master/retry counts and stable recovery reasons. Inspect the trace even on success when diagnosing a recovered connection. Do not infer health of a proxy, gateway or training environment from SSH success alone. Traces omit secrets/commands and do not profile unrelated cloud HTTP operations. `check --targets a,b --report /tmp/diagnostic.json --json` preserves per-target partial checks and exports only ordinal target IDs, safe steps/timings/codes and trace, not endpoint/path/tool details. `error.failure_kind` classifies observed transport evidence without guessing network ownership.

`SUBMISSION_UNKNOWN` (operation exit 3) includes the original saved ID. Query that ID; do not automatically replace or replay the run. An unreachable machine or `unknown` result is not proof of training failure. `status` lists can contain successful rows alongside query errors.

For bounded history inspection, use `status --status failed --limit 50 --json`; repeat `--status` to include more states. `--since`/`--until` take RFC3339 submission-attempt timestamps, not completion times. A list `--target` filters saved aliases, including removed ones; it does not redirect an ID query. Inspect scan coverage and unresolved rows before interpreting an empty match list as no failures.

`machine list --json` / `machine show NAME --json` inspects saved connection settings locally. `machine remove NAME --json` only removes that alias; it neither deletes remote data nor discards old run bindings. Connection changes use `machine add --replace`. A missing current alias requires explicit reselection, not a guessed fallback.

Use explicit argv after `run --` for changing experiment parameters. Optional project `run.command` and `run.setup` are Shell commands; setup runs before each task and its failure prevents training. Setup's `cd`, exports and activation do not persist into the separate training process. Use an explicit interpreter or `uv run`.

## First SSH trust

Provider-backed commands apply the [first-use policy](../SKILL.md#first-ssh-trust)
inside their original SSH connection, including JSON and batch calls. Do not open
another terminal, request an extra approval or retry a purchase to record a new
key. Explicit per-target host-key settings still take precedence.

A changed known key requires investigation; never remove the old entry or disable
verification automatically. For static SSH targets, retain the user's configured
trust policy; an authorized native first-use prompt may be answered `yes`.
Connection recovery still observes the same Run/request/instance. See the
[OpenSSH host-key policy](https://man.openbsd.org/ssh_config#StrictHostKeyChecking).

## Stop one task

When stopping is authorized, use the saved run ID:

```bash
loaf stop <run-id> --json
```

Loaf requests process-group cleanup and waits for a confirmed result. Do not look up PIDs or send signals yourself. An already completed task returns its original outcome, including a natural failure.

`STOP_UNCONFIRMED` (operation exit 3) means query the same ID with `status`; do not report the task as stopped. Canceling the client does not withdraw an accepted stop request. Repeating stop does not reset or replace its first grace policy.

Runner operations automatically deploy and verify the exact helper packaged with the local CLI. Compatible formal `1.x` control helpers and older Supervisors/Coordinators can coexist; deployment does not replace an active Supervisor. `VERSION_MISMATCH` identifies an unsupported pairing, not every difference in software strings. Do not bypass format checks or edit saved origin versions to force access.

## Formal compatibility and development state

The supported baseline begins at formal `v1.0.0`. Newer formal `1.x` builds share
`stable-v1/state.db`; development and RC builds use `state-v4/state.db` and do not
inherit that promise. Local/project schemas are 4, machine queue schema is 2,
and each database records its formal/development series. Moving a database does
not change that series. Original stores and credentials are not deleted.

Ordinary work reuses the intended local project and compatible remote root; do
not create a new project directory for each experiment. The first formal use
requires a fresh managed remote project root, not relocation of the local repo.
Remote project control stays in `.loaf-control-v2`; the machine queue stays at
its original single-ledger location and refuses a development ledger. Do not
create a parallel ledger or remove a live Coordinator's files to bypass refusal.

Do not import `v0.x`, development or RC records, modify format metadata, or
hot-upgrade active Supervisors. Keep original tools for unsupported historical
work. Reconcile older uncertain paid requests before another authorized
acquisition; an empty formal local store is not proof of no prior allocation.
For an explicitly installed Bridge, stop the old process and restart the new
binary with the same formal state directory. Resume saved cursors and preserve
unconfirmed evidence rather than automatically resending it.
