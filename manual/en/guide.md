# Run and retrieve an experiment

[Home](../../README.md) · [Command reference](commands.md) · [简体中文](../zh-Hans/guide.md)

## Set up a research machine

From your research project directory:

```bash
# Register a platform-provided SSH connection
loaf machine add autodl --ssh "ssh -p 12345 root@example.com"
loaf machine use autodl

# Optional: inspect the connection, remote directory, tools, and disk
loaf check
```

Already have an SSH Host alias? Use `loaf machine add lab --ssh lab-gpu`. Connections involving jump hosts remain in your SSH Config; `-J`, keys, ports, and an explicit `-F` config file are also accepted. Pasted input must contain only connection arguments, not a remote command or Shell expression. To change an existing alias, use `--replace`.

List saved machines with `loaf machine list`, inspect one with `loaf machine show lab`, and remove an alias with `loaf machine remove lab`. These commands only read or update local settings, not remote machines. Removing an alias leaves saved runs and remote data intact. A project still selecting that alias requires explicit reselection; its TOML is not rewritten and another machine is never chosen implicitly.

`machine add` saves the connection locally; `check` actually connects. JSON and non-TTY commands do not ask for passwords or host trust. Establish host trust with native SSH first. For these static targets, authentication must then work through a key, SSH agent, or native SSH connection sharing; a one-off password login without connection sharing does not authenticate later commands. Loaf never saves passwords or disables host-key verification.

Need a new AutoDL instance? See [Acquire and connect AutoDL compute](cloud.md).

### Check runner deployment explicitly

```bash
loaf check --runner
```

Normal `check` only observes the remote environment. `--runner` additionally uploads the matching bundled executable into a private, versioned Loaf directory and verifies its version. It does not start an experiment or install Python, uv, CUDA, or project dependencies.

Python, uv, and GPU tools are optional observations. Their absence does not make a CPU-capable machine fail the check. Missing rsync, unsupported platforms, or an unwritable remote location do.

### Check several targets and export diagnostics

```bash
loaf check --targets lab-a,lab-b --timeout 20s --trace --report /tmp/loaf-diagnostic.json --json
```

`--targets` checks 1–32 distinct aliases without `--target`. Each has its own timeout; machine mode uses at most four concurrent checks and interactive authentication stays serial. Failed/unexamined rows retain successful observations. Per-target steps distinguish connection, authentication, platform and runner checks. Ordinary check is read-only remotely; helper deployment/repair still requires `--runner`.

`error.failure_kind` and trace distinguish authentication, host trust, name resolution, refused, timed out, reset, closed and unclassified transport failures without attributing them to a gateway or local network. A retry after a failed first connection is not counted as reconnection of an established master.

`--report` atomically writes a private 0600 allowlisted diagnostic: target input ordinals, known platform labels, step outcomes, stable failure categories, timings and safe trace. It omits aliases, endpoints, paths, commands, request bodies, environments and credentials; ordinary check JSON keeps its useful diagnostics. The report destination must be absent or an existing known-format Loaf diagnostic, never a symlink or unrelated file. Safe trace collection works even without `--trace`.


## Run an experiment

From the project directory, submit the command you would otherwise run remotely:

```bash
loaf run -- python3 train.py
loaf status
loaf logs
```

`run` checks execution prerequisites, deploys the matching runner, synchronizes working sources, and copies an independent work directory under the same project lock. It then starts a detached supervisor and returns a run ID. There is no required `check`, `sync`, project initialization, or launch script before each run. Later syncs do not change an existing run's code.

Acceptance means the supervisor owns the task, not that training succeeded. Close the local terminal or SSH connection and query the saved ID later:

```bash
loaf status <run-id>
loaf logs <run-id> -f
```

`status` lists the latest submitted or submission-uncertain runs in this project, bounded to 20 by default (`--limit` or `[ps].limit` changes the bound). An ID queries that run only. Failed queries preserve successful rows and do not label training as failed. `logs` without an ID selects the latest submitted or submission-uncertain run, not an older fallback. Proven preflight/sync failures do not replace it. Saved runs keep their original connection and remote location when project defaults change; `--target` cannot redirect an ID query.

If preparation fails after saving a run ID locally, the error result retains that ID and its original binding. A saved `submission: "preparing"` record can be read with `inspect <run-id>` or `status <run-id> --local`; it stays out of history lists and latest-run selection. A live `status <run-id>` queries the same binding without starting or resuming execution. Preparation is not confirmed submission.

Runner operations require the same software version as the local CLI. Loaf automatically deploys and verifies its bundled runner, repairing a missing or mismatched executable. It does not download an arbitrary release or hot-replace a running supervisor. Tasks created by another version return `VERSION_MISMATCH`; retain their matching CLI alongside an upgrade and invoke it explicitly for those tasks. Unknown record formats are rejected without rewriting them.

### Preview submission entirely offline

```bash
loaf run --dry-run --json -- python3 train.py
```

The plan includes target selection, literal entry-point argv, setup presence, environment names (not values), selected working files/source bytes, outputs, waiting and sync/download policies, and per-field `flag`/`project`/`user`/`default` provenance (`local_selection` for `loaf machine use`). An unregistered project gets no ID. Preview does not access credentials, SSH, helpers or state writes. It is neither a frozen snapshot nor a transfer estimate or proof of remote prerequisites. Actual submission still checks prerequisites and synchronizes automatically.

### Find tasks and inspect them offline

Give a task a readable name and repeatable tags when submitting:

```bash
loaf run --name "ablation-seed1" --tag ablation --tag seed1 -- python3 train.py
loaf status --name "ablation-seed1" --tag seed1 --json
loaf status --local --tag ablation --json
loaf inspect <run-id> --json
```

Names and tags are saved locally at submission and remain immutable. Names need not be unique; the run ID remains the identity used for task operations. Name/tag filters match exact values before the bounded queries; repeated tags require all values. They do not change the command, remote environment, paths or latest-run selection.

`status --local`, including an explicit ID, and `inspect <run-id>` read saved facts without SSH, cloud lookup, credential access or helper deployment. They also work with removed aliases and older software versions recorded in the current SQLite format; pre-v0.5 JSON history is not imported. JSON marks `source: "local"`; execution status is `unknown` unless a retained cleanup receipt already supplies a terminal result. Local mode rejects live `--status` filters and nonzero `--wait`.

`inspect` includes the saved `version`, current `client_version`, `compatible`, frozen machine/instance identity, submission confirmation, locations and output paths. It excludes SSH arguments, credential references and private execution environments. A saved-task version error also returns `error.expected_version` and `error.actual_version`. Keep old executables under versioned names, such as `loaf-v0.4.0-rc.1`, rather than overwriting the only CLI able to control an active task. These commands do not download a CLI or migrate a task.

### Filter task history

```bash
loaf status --status failed --status stopped --limit 50 --json
loaf status --since 2026-10-01T00:00:00Z --target lab --json
```

`--since` and `--until` use RFC3339 timestamps and filter the saved submission-attempt time, not completion time. A list's `--target` matches the saved alias, including removed aliases. Time and alias filters run before the bounded remote queries; status filters apply to those observations without searching arbitrarily far back to fill the limit. JSON reports how many entries were eligible, examined, matched, unresolved, or left unexamined. Failed observations remain visible with `query_error`, even when filtering statuses. A single-ID query rejects list filters.

### Short tasks: wait for a result, not forever

```bash
loaf run --wait 10s --json -- python3 smoke.py
loaf status <run-id> --wait 30s --json
```

Waiting starts after remote acceptance. A task that finishes within the budget returns its final status, exit result, and bounded log tail in the same response. Otherwise Loaf returns the observed state and the same run ID; the task continues. `--wait 0s` returns after acceptance. The maximum wait is 5 minutes. This is an observation budget, not a training timeout; canceling observation does not stop accepted work. JSON keeps `submission: "submitted"` after confirmed acceptance, even if a later observation returns `status: "unknown"` and an error. `submission: "uncertain"` means acceptance still needs confirmation against that same ID.

Normal output shows preparation stages and streams logs during `run --wait`. JSON mode returns one finite result without progress text. A following log stream uses NDJSON:

```bash
loaf logs <run-id> --tail 100 --json
loaf logs <run-id> -f --json
```

Log tails default to 100 lines and at most 256 KiB of log text. `log.truncated` reports omitted output. Following reads emit `data.type = "chunk"` and a final `"end"`, with the run observation under `data.run`; stream errors emit `"error"` with the ordinary error object. Offsets count bytes in the source log. Program output is not translated or interpreted as metrics. Programs must flush their own output for timely logs.

For a live log stream that should survive one transient disconnect, opt in:

```bash
loaf logs <run-id> -f --reconnect
loaf logs <run-id> -f --resume "$cursor" --json
```

Finite log queries return `data.log.cursor`; stream chunks return `data.run.log.cursor`. Save the cursor only after consuming its chunk. `--resume` requires `--follow`, cannot be combined with an explicit `--tail`, and continues at that confirmed source-byte position. It also permits one transient reconnection, as does `--reconnect`. Default `logs -f` retains its single-connection behavior.

A cursor is bound to the software version, task, location and log file, with a boundary-integrity guard. Replacement, truncation or rewriting at the saved boundary returns `LOG_CHANGED`, never a silent restart from the tail. Read a fresh tail explicitly to obtain a new cursor when that happens. Output-consumer errors, cancellation, authentication/trust failures and an already-delivered end event do not trigger a restart. Accepted tasks continue independently.

If submission confirmation is lost, Loaf checks the same saved ID once. If still uncertain, it returns `SUBMISSION_UNKNOWN` with that ID and operation exit 3. Query it; do not automatically submit another run.

### Recover sessions and wait for several tasks

```bash
loaf logs <run-id> -f --resume-file /tmp/loaf-cursor.json --recovery-budget 2m
loaf wait r_A r_B --timeout 30m --json
loaf wait --tag ablation --timeout 1h
```

`--resume-file` loads an acknowledged cursor and enables recovery. Human terminal output checkpoints only after a successful write. JSON and piped output **never automatically acknowledge processing**: handle a chunk, then save its `data.run.log.cursor` with `loaf logs <run-id> --resume-file /tmp/loaf-cursor.json --ack-cursor "$cursor" --json`. This acknowledgement is offline. Keep cursor files outside source trees. Writes are atomic and cannot rewind a saved stream; unknown formats and symlinks are rejected.

Without an explicit `--recovery-budget`, opt-in recovery still retries once. A positive budget allows consecutive transient failures within a total disconnected-wait budget (up to 24h); healthy streaming does not consume it. Human recovery messages go to stderr. Default `logs -f` is unchanged. Sink failure, cancellation, trust/authentication failure, end and `LOG_CHANGED` never restart the stream.

Successful observations save a public `last_observation` in SQLite with its observation time, phase and exit result; `last_query_failure` retains only the most recent failed query's time, code and stage. Offline `status --local` and `inspect` show these dated facts, not a live status. No log text or execution environment is cached.

A local state or cursor-checkpoint write failure is reported separately as `state_error`, containing a stable error code. History lists and `wait` count these as `state_errors`, not query errors. Local-save and output-consumer failures can fail the operation without discarding a verified remote status or exit result, and do not become `last_query_failure`. A log chunk whose consumer failed is not acknowledged.

`wait` freezes explicit IDs or the exact tag-matched local set at invocation (1–1000 runs), polls their saved bindings, and returns every row with completed/pending/query-error and examined/unexamined coverage. Unreachable tasks do not prevent observation of others. Human output reports changes; JSON is one finite result. If the total budget interrupts a later query, the row retains its previous completed observation with `observed_at` and `query_interrupted`; `interrupted` counts these rows rather than inventing a target failure. Timeout or cancellation leaves all tasks running. This does not schedule workloads.

### Optional project setup

```toml
[run]
command = "uv run python train.py"
setup = "uv sync"                 # Explicitly run before each task
wait = "10s"

[run.env]
EXPERIMENT_NAME = "baseline"

[logs]
tail = 100
```

With a configured command, use `loaf run`. Explicit argv after `run --` replaces the configured command and receives no extra Shell interpretation. TOML `run.command` and `run.setup` are `/bin/sh -c` commands. Setup runs once per task, and failure prevents training. Its `cd`, exports, and environment activation do not persist into the separate command process; use an explicit interpreter or `uv run`. Project `run.env` applies to both processes and is not echoed in Loaf's observations; the program itself can still print it.

Loaf does not install Python or uv, guess environment activation, or upload datasets. Provision those explicitly. Data/shared environments can live beside `cache/`; refer to them by absolute paths because execution happens in `<remote_dir>/runs/<run-id>/work/`. Output paths are frozen at submission and can be collected with `loaf pull`.

Try the [dependency-free CPU example](../../examples/cpu/) or the [explicit setup example](../../examples/setup/). Agents can use the short [Loaf Skill](../../skills/loaf/SKILL.md); they do not need to inspect internal files, PIDs, or locks.

### Train and use a small GPU model

The [PyTorch example](../../examples/torch/) trains a classifier on generated data without downloads. It requires an existing CUDA-capable PyTorch environment. From that example directory, select your machine and use `loaf run`, then `loaf logs` and `loaf pull` as above. If `python3` is not on the noninteractive SSH PATH, explicitly select your existing interpreter in `run.command` or after `run --`; Loaf does not source Shell startup files or activate guessed environments.

Outputs include a PyTorch checkpoint, metrics, model parameters, and held-out inference cases. The training script reloads its checkpoint. Run `python3 verify.py` with the downloaded run directory to independently use the collected parameters for inference on your laptop; this verification needs only Python's standard library, not local PyTorch. GPU examples are opt-in and never run in the CPU-only CI fixture.


## Inspect a machine and forward a service

```bash
loaf exec -- nvidia-smi
loaf ssh
loaf forward 6006 --local-port 16006
```

`exec` is a foreground machine command: output appears as it is produced, and the result includes the remote exit code. It defaults to the remote user's home; `--cwd` selects another directory, with relative paths resolved from that home. It does not synchronize code, run setup, create a task ID, or promise durable execution. Use `run`, optionally with `--wait`, for experiments that need those capabilities. Arguments after `exec --` receive no implicit Shell interpretation; invoke a Shell explicitly when needed.

`exec --json` emits bounded NDJSON output chunks and a final result/error. Chunks identify stdout/stderr and preserve source bytes with text or base64 encoding. A confirmed nonzero remote exit returns `EXEC_FAILED` (operation exit 1); an SSH 255 result or canceled observation is conservatively `EXEC_UNCONFIRMED` (exit 3). Do not automatically replay a command whose result is unconfirmed.

`ssh` opens the selected machine's native interactive terminal, not a running experiment. `forward` uses native OpenSSH to expose an already-running remote loopback service on a local loopback port; omit `--local-port` to use the same port on both ends. The example exposes remote port 6006 at `http://127.0.0.1:16006`. It does not start TensorBoard/Jupyter, and a listening tunnel does not prove the application is healthy.

Forwarding stays in the foreground and uses a dedicated connection with SSH backgrounding/connection sharing disabled. Exiting removes this forward without closing another SSH master or stopping training. Keys, agents, jump hosts, and host-key checks still apply; a noninteractive dedicated connection cannot borrow authentication from a separate password-only master. Interactive `ssh` and `forward` reject `--json`.


## Stop an experiment

```bash
loaf stop                    # Latest submitted run in this project
loaf stop <run-id> --json
```

Stop uses the run's saved machine and location, even after project defaults change. It asks the owning supervisor to terminate the managed process group, waits for confirmation, and retains logs and actual exit results. A stop accepted during setup prevents the training phase from starting. Processes that deliberately leave the managed group are outside this cleanup scope.

New runs allow 10 seconds between TERM and KILL. Set a project or user preference, or override it for one request:

```toml
[stop]
grace = "10s"
```

```bash
loaf stop <run-id> --grace 30s
```

Precedence is the explicit flag, project setting, user setting, then the runner default. Grace accepts 0s to 5m; `0s` allows immediate escalation. The first accepted request fixes the grace interval; repeated requests do not reset or change it. Stopping an already completed run returns its original result.

Confirmation waits for up to grace plus 5 seconds, capped at 5 minutes, in addition to SSH connection time. A timeout, lost confirmation, or canceled client returns `STOP_UNCONFIRMED` with operation exit 3; an accepted remote request continues. Check the same ID with `loaf status <run-id> --json`. An unknown supervisor identity is reported rather than signaling a possibly reused PID. JSON includes `data.stop_grace_millis` when the accepted policy is known.


## Retrieve experiment outputs

```bash
loaf pull                    # Latest submitted run in this project
loaf pull <run-id> --json
loaf pull <run-id> --path outputs --path checkpoints --to ./export
```

Default outputs are `outputs/`, or the `[outputs].paths` saved when that run was submitted. Later edits to project outputs or the default machine do not redirect an existing run. Repeat `--path` to replace the selection for this invocation; paths are literal work-relative subpaths, not Shell globs. No separate check or stop is required.

Downloads go to `<project>/loaf-results/<run-id>/` and preserve output subpaths. `--to` names the exact destination without an extra run-ID directory; relative paths use the current directory. Root, home, and directories containing the project are rejected as destinations. Repeating pull updates changed files without deleting local extras or remote outputs. Files are replaced after transfer, not written in place; after cancellation or failure, retry the same pull. Canceling pull does not stop training.

Project and user configuration support these independent preferences:

```toml
[pull]
base_dir = "loaf-results"
checksum = false
```

Relative `base_dir` values use the project root, and Loaf appends the run ID. Choose a separate result base, not the project root or an ancestor. The configured base inside the project is excluded from source uploads, even when tracked. For a one-off `--to` inside the project, add that destination to `.gitignore` or `sync.exclude` if it should not be uploaded.

Default change detection compares size and modification time. Enable `--checksum` to detect content changes with the same size and timestamp, at the cost of reading files on both ends. Explicit flags override project preferences, then user preferences; `--checksum=false` overrides an enabled preference. `--to` overrides the default destination.

Missing paths produce `OUTPUT_MISSING` (exit 1). Existing selections are downloaded when possible; JSON retains `paths`, `downloaded_paths`, `missing_paths`, destination, and effective policy in `data` alongside the error. `changed_files` is present only after a successful rsync transfer without a recovery retry; a recovered transfer omits it rather than reporting only the last attempt's changes. An all-missing selection is never an empty success. Unsafe paths or special files reject the selection before transfer; relative links must resolve within the selected trees. Existing destination links cannot redirect writes outside the result directory.

Running, failed, and stopped tasks can all have useful outputs. A running pull copies current contents, not a consistent checkpoint snapshot; wait for completion or have training publish checkpoints atomically when consistency matters. Two downloads to the same destination are rejected with `PULL_BUSY`; downloads coordinate with cleanup through a separate data lock, without holding the remote source-sync or execution-control locks.

### Browse and compare several outputs

```bash
loaf outputs <run-id> --depth 2 --limit 200 --json
loaf outputs <run-id> --prepare-runner --json  # Explicit helper deployment/repair
loaf pull r_A r_B --into ./comparison --json
loaf pull r_A r_B --into ./comparison --concurrency 2 --json
```

`outputs` reads only each run's frozen output selection. It reports relative paths, type, bytes, modification time and missing roots, with visited-root and entry coverage. Depth is relative to each output root (root = 0); limits are 0–20 levels and 1–1000 entries. `truncated` and `depth_limited` make unvisited contents explicit. It does not read file contents, checksum files, follow links or require a successful/live task. Ordinary browsing does not upload a helper; a missing/mismatched executable returns `RUNNER_REQUIRED`. Only `--prepare-runner` authorizes repair. `pull` keeps its existing automatic preparation.

Multiple explicit IDs require `--into`; each downloads under `<into>/<run-id>/` using its own saved connection and output paths. JSON retains every result/error with completed, failed and unexamined counts; one failure does not discard successful downloads. Downloads are serial by default; `--concurrency 1–4` is explicit and interactive authentication stays serial. Repeating is incremental and preserves local extras. Single-run `--to` still names the exact directory and cannot be combined with batch `--into`. One-off download destinations need normal ignore/exclude rules if they sit in your source tree.


## Reclaim completed task data

```bash
loaf clean <run-id> --dry-run --json
loaf clean <run-id>
loaf clean <run-id> --yes --json
```

Cleanup requires explicit IDs; it never selects the latest task or all tasks implicitly. The preview names the bound machine and remote data that would be removed without deploying a helper or deleting files. Actual cleanup rechecks ownership, completion, and locks; a preview is not a reserved deletion permit. Human output asks once for confirmation. JSON and noninteractive deletion require `--yes`.

Cleanup removes the selected task's remote working files, logs, and outputs. Collect anything you need first: downloaded local results are kept, but remote payload deletion cannot be undone. Only confirmed submissions with a freshly verified terminal result are eligible. Active, submission-uncertain, unowned, or unconfirmed tasks are refused rather than stopped implicitly. Unsubmitted preparation data (`submission: "preparing"`) cannot currently be reclaimed with `clean`. Other runs, source caches, shared data, and environments are not removed.

A small deletion receipt and ID claim remain to prevent a delayed request from executing the same task again. `status` retains the original execution outcome and reports cleanup metadata; `logs`, `pull`, and `stop` return `RUN_CLEANED`. Cleaning the newest run does not make those commands silently select an older one. For partial failure or lost confirmation, inspect the reported IDs and retry only those same explicit IDs.


## Sync working code

From the project directory, preview the selected files before uploading:

```bash
loaf sync --dry-run
loaf sync
```

The preview is local-only: it needs no configured target, SSH connection, or runner and creates no project state. A real sync checks the target, deploys the matching bundled runner, and invokes system rsync over OpenSSH. It does not install experiment dependencies or start training.

Sources go to `<remote_dir>/cache/`. The first sync requires a new or empty project directory; subsequent syncs verify its project marker. Do not select an existing research directory containing data or environments. Keep those beside `cache/`, not inside it.

The default **mirror** mode removes deleted and newly excluded source paths from `cache/` only. Other project directories and existing run copies are untouched. A project lock prevents overlapping source updates and run-copy preparation. Failed or canceled transfers leave the cache unavailable for new runs until an explicit sync succeeds; they do not stop existing runs. The cache is updated incrementally, not as an atomic directory swap.

### Choose what to upload

- In Git projects, native Git selects tracked working files and nonignored untracked files, including uncommitted edits. Git tracking and nested ignore/negation rules are preserved. Non-Git projects read nested `.gitignore` files.
- Loaf additionally excludes virtual environments, common caches, `.git`, `.env`/`.env.*`, common private-key names, `*.pem`, `*.key`, `*.pt`, `*.pth`, `*.ckpt`, `*.safetensors`, and `loaf-results/`, even when tracked. Configured output paths and the current local result base are excluded; default outputs are `outputs/`.
- Safe relative symlinks to selected project files/directories are preserved without following directory links. Absolute, escaping, broken, and excluded-target links are skipped and reported. Nested repositories/submodules are not expanded; synchronize them separately.
- `sync.exclude` adds positive Gitignore-style patterns. It cannot reinclude default exclusions with `!`. The preview lists exclusions and their reasons. Defaults are name-based safeguards, not a secret scanner.

`selected_bytes` counts selected regular-file sizes, not network traffic. `changed_files` counts transferred file contents and changed link targets, excluding deletions and timestamp/permission-only updates. The preview selects current working paths; it does not freeze local files while you edit them.

## Construct arguments without guessing

```bash
loaf run -- python3 train.py --label 'seed 1' --rate -1 --json
loaf pull ID --path 'outputs/a,b.csv' --path 'outputs/seed 1.pt'
loaf status --tag ablation --tag seed1
loaf sync --dry-run --respect-gitignore=false
loaf run --wait 0s -- python3 train.py
```

`--json` after the `run --` separator belongs to the program. A quoted empty
string remains one empty argv element. `--path`, `--tag`, `--status` and
`--exclude` take literal values and repeat for additional values; `--path` does
not expand glob patterns. `check --targets`, cloud `--gpu-model` and `--region`
accept CSV and repeated flags. Use a single `--region=` to replace configured
regions with an explicit empty list when the operation permits it.

Boolean overrides use `--checksum=false` or `--respect-gitignore=false`.
Omission inherits lower-precedence settings; false, zero and an empty value are
not interchangeable with omission. `--wait 0s` explicitly disables waiting,
whereas `status --limit 0` is invalid. Durations need units (`10s`, `2m`), memory
floors need `GiB`, and compute price caps are decimal strings, not binary floats.

For Shell syntax, invoke a Shell explicitly:

```bash
loaf exec -- /bin/sh -c 'printf "%s\n" "$HOME"'
```
