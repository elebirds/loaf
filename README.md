# Loaf

[English](README.md) | [简体中文](README.zh-Hans.md)

**Write code locally. Use your existing SSH research machines.**

Loaf synchronizes working code to your SSH machines and runs experiments that survive disconnection, with saved task IDs, logs, and exit results. Reuse your SSH configuration, keys, and jump hosts without handing credentials to another service.

> Current source includes v0.2 remote operations and explicit AutoDL rental/connection commands for v0.3. The published [v0.1.0-rc.2 archive](https://github.com/elebirds/loaf/releases/tag/v0.1.0-rc.2) provides the core experiment workflow; build from current source for newer commands.

## Install

Download the archive for your platform and its checksums file from [Releases](https://github.com/elebirds/loaf/releases). Filenames use `darwin` for macOS, `linux` for Linux, `arm64` for Apple Silicon/ARM64, and `amd64` for Intel/AMD x86-64. Extract the archive into a new directory.

Archives contain one executable, both READMEs, the Skill, examples, `BUILD.json`, and third-party license texts. From an extracted archive directory:

```bash
mkdir -p "$HOME/.local/bin"
install -m 755 loaf "$HOME/.local/bin/loaf"
"$HOME/.local/bin/loaf" version --json
```

Add that directory to PATH if needed. Running an archive needs OpenSSH and rsync locally, not Go. The remote machine needs Linux and rsync; prepare the experiment's Python environment separately.

The build targets macOS/Linux amd64 and arm64. Full SSH workflows have been exercised with macOS arm64 and Linux arm64 clients locally, and Linux amd64 clients in GitHub CI. The macOS amd64 client is cross-built. Linux amd64 runners have completed real GPU training, and Linux arm64 runners are exercised by the CPU fixture. Cross-builds alone are not runtime validation.

`BUILD.json` records the source revision, modified-tree state, Go version, target, and dependency notice inventory. `loaf version --json` exposes the corresponding build identity; missing source metadata is reported as unknown, not clean. Set `archive` to the exact archive filename and `version` to the software version in that filename. Then run the check from the directory containing the archive and its checksums file:

```bash
# archive is the exact filename of your archive
grep -F "  $archive" "loaf_${version}_checksums.txt" | shasum -a 256 -c -
```

Published release archives carry GitHub build-provenance attestations, verifiable with `gh attestation verify "$archive" --repo elebirds/loaf`. See [GitHub's verification guide](https://docs.github.com/en/actions/how-tos/secure-your-work/use-artifact-attestations/use-artifact-attestations#verifying-artifact-attestations-with-the-github-cli). Local builds include metadata and download checksums, not a GitHub-signed attestation.

### Build from source

Requires Go 1.27.3, Git, OpenSSH, and rsync. The Go version is pinned in `mise.toml`; review that file before trusting it if you use mise.

```bash
bash scripts/build.sh
export PATH="$PWD/dist:$PATH"
loaf --help
```

The build embeds Linux amd64 and arm64 runners and produces `dist/loaf`, a platform archive, and `loaf_<version>_checksums.txt`. For other build targets, set `LOAF_BUILD_TARGETS`, for example `linux/amd64 linux/arm64 darwin/amd64 darwin/arm64`.

## Set up a research machine

From your research project directory:

```bash
# Register a platform-provided SSH connection
loaf add autodl --ssh "ssh -p 12345 root@example.com"
loaf use autodl

# Optional: inspect the connection, remote directory, tools, and disk
loaf check
```

Already have an SSH Host alias? Use `loaf add lab --ssh lab-gpu`. Connections involving jump hosts remain in your SSH Config; `-J`, keys, ports, and an explicit `-F` config file are also accepted. Pasted input must contain only connection arguments, not a remote command or Shell expression. To change an existing alias, use `--replace`.

List saved machines with `loaf targets`, inspect one with `loaf targets lab`, and remove an alias with `loaf remove lab`. These commands only read or update local settings, not remote machines. Removing an alias leaves saved runs and remote data intact. A project still selecting that alias requires explicit reselection; its TOML is not rewritten and another machine is never chosen implicitly.

`add` saves the connection locally; `check` actually connects. JSON and non-TTY commands do not ask for passwords or host trust. Establish host trust with native SSH first. For these static targets, authentication must then work through a key, SSH agent, or native SSH connection sharing; a one-off password login without connection sharing does not authenticate later commands. Loaf never saves passwords or disables host-key verification.

Need a new AutoDL instance? See [Acquire and connect AutoDL compute](#acquire-and-connect-autodl-compute).

### Check runner deployment explicitly

```bash
loaf check --runner
```

Normal `check` only observes the remote environment. `--runner` additionally uploads the matching bundled executable into a private, versioned Loaf directory and verifies its version. It does not start an experiment or install Python, uv, CUDA, or project dependencies.

Python, uv, and GPU tools are optional observations. Their absence does not make a CPU-capable machine fail the check. Missing rsync, unsupported platforms, or an unwritable remote location do.

## Run an experiment

From the project directory, submit the command you would otherwise run remotely:

```bash
loaf run -- python3 train.py
loaf ps
loaf logs
```

`run` checks execution prerequisites, deploys the matching runner, synchronizes working sources, and copies an independent work directory under the same project lock. It then starts a detached supervisor and returns a run ID. There is no required `check`, `sync`, project initialization, or launch script before each run. Later syncs do not change an existing run's code.

Acceptance means the supervisor owns the task, not that training succeeded. Close the local terminal or SSH connection and query the saved ID later:

```bash
loaf ps <run-id>
loaf logs <run-id> -f
```

`ps` lists the latest submitted or submission-uncertain runs in this project, bounded to 20 by default (`--limit` or `[ps].limit` changes the bound). An ID queries that run only. Failed queries preserve successful rows and do not label training as failed. `logs` without an ID selects the latest submitted or submission-uncertain run, not an older fallback. Proven preflight/sync failures do not replace it. Saved runs keep their original connection and remote location when project defaults change; `--target` cannot redirect an ID query.

Runner operations require the same software version as the local CLI. Loaf automatically deploys and verifies its bundled runner, repairing a missing or mismatched executable. It does not download an arbitrary release or hot-replace a running supervisor. Tasks created by another version return `VERSION_MISMATCH`; finish them with their matching CLI before upgrading. Unknown record formats are rejected without rewriting them.

### Filter task history

```bash
loaf ps --status failed --status stopped --limit 50 --json
loaf ps --since 2026-10-01T00:00:00Z --target lab --json
```

`--since` and `--until` use RFC3339 timestamps and filter the saved submission-attempt time, not completion time. A list's `--target` matches the saved alias, including removed aliases. Time and alias filters run before the bounded remote queries; status filters apply to those observations without searching arbitrarily far back to fill the limit. JSON reports how many entries were eligible, examined, matched, unresolved, or left unexamined. Failed observations remain visible with `query_error`, even when filtering statuses. A single-ID query rejects list filters.

### Short tasks: wait for a result, not forever

```bash
loaf run --wait 10s --json -- python3 smoke.py
loaf ps <run-id> --wait 30s --json
```

Waiting starts after remote acceptance. A task that finishes within the budget returns its final status, exit result, and bounded log tail in the same response. Otherwise Loaf returns the observed state and the same run ID; the task continues. `--wait 0s` returns after acceptance. The maximum wait is 5 minutes. This is an observation budget, not a training timeout; canceling observation does not stop accepted work.

Normal output shows preparation stages and streams logs during `run --wait`. JSON mode returns one finite result without progress text. A following log stream uses NDJSON:

```bash
loaf logs <run-id> --tail 100 --json
loaf logs <run-id> -f --json
```

Log tails default to 100 lines and at most 256 KiB of log text. `log.truncated` reports omitted output. Following reads emit `data.type = "chunk"` and a final `"end"`, with the run observation under `data.run`; stream errors emit `"error"` with the ordinary error object. Offsets count bytes in the source log. Program output is not translated or interpreted as metrics. Programs must flush their own output for timely logs.

If submission confirmation is lost, Loaf checks the same saved ID once. If still uncertain, it returns `SUBMISSION_UNKNOWN` with that ID and operation exit 3. Query it; do not automatically submit another run.

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

Try the [dependency-free CPU example](examples/cpu/) or the [explicit setup example](examples/setup/). Agents can use the short [Loaf Skill](skills/loaf/SKILL.md); they do not need to inspect internal files, PIDs, or locks.

### Train and use a small GPU model

The [PyTorch example](examples/torch/) trains a classifier on generated data without downloads. It requires an existing CUDA-capable PyTorch environment. From that example directory, select your machine and use `loaf run`, then `loaf logs` and `loaf pull` as above. If `python3` is not on the noninteractive SSH PATH, explicitly select your existing interpreter in `run.command` or after `run --`; Loaf does not source Shell startup files or activate guessed environments.

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

Confirmation waits for up to grace plus 5 seconds, capped at 5 minutes, in addition to SSH connection time. A timeout, lost confirmation, or canceled client returns `STOP_UNCONFIRMED` with operation exit 3; an accepted remote request continues. Check the same ID with `loaf ps <run-id> --json`. An unknown supervisor identity is reported rather than signaling a possibly reused PID. JSON includes `data.stop_grace_millis` when the accepted policy is known.

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

Missing paths produce `OUTPUT_MISSING` (exit 1). Existing selections are downloaded when possible; JSON retains `paths`, `downloaded_paths`, `missing_paths`, destination, and effective policy in `data` alongside the error. `changed_files` is present only after a successful rsync transfer. An all-missing selection is never an empty success. Unsafe paths or special files reject the selection before transfer; relative links must resolve within the selected trees. Existing destination links cannot redirect writes outside the result directory.

Running, failed, and stopped tasks can all have useful outputs. A running pull copies current contents, not a consistent checkpoint snapshot; wait for completion or have training publish checkpoints atomically when consistency matters. Two downloads to the same destination are rejected with `PULL_BUSY`; downloads coordinate with cleanup through a separate data lock, without holding the remote source-sync or execution-control locks.

## Reclaim completed task data

```bash
loaf clean <run-id> --dry-run --json
loaf clean <run-id>
loaf clean <run-id> --yes --json
```

Cleanup requires explicit IDs; it never selects the latest task or all tasks implicitly. The preview names the bound machine and remote data that would be removed without deploying a helper or deleting files. Actual cleanup rechecks ownership, completion, and locks; a preview is not a reserved deletion permit. Human output asks once for confirmation. JSON and noninteractive deletion require `--yes`.

Cleanup removes the selected task's remote working files, logs, and outputs. Collect anything you need first: downloaded local results are kept, but remote payload deletion cannot be undone. Active, submission-uncertain, unowned, or unconfirmed tasks are refused rather than stopped implicitly. Other runs, source caches, shared data, and environments are not removed.

A small deletion receipt and ID claim remain to prevent a delayed request from executing the same task again. `ps` retains the original execution outcome and reports cleanup metadata; `logs`, `pull`, and `stop` return `RUN_CLEANED`. Cleaning the newest run does not make those commands silently select an older one. For partial failure or lost confirmation, inspect the reported IDs and retry only those same explicit IDs.

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

## Project configuration

Projects are detected from the nearest `loaf.toml`, then the Git worktree root, then the current directory. No `init` is required. An optional project file can select a machine, directory, and sync policy:

```toml
target = "autodl"
remote_dir = "~/projects/my-research"

[sync]
mode = "mirror"            # Or "update" to keep removed/excluded remote files
respect_gitignore = true
exclude = ["data/", "checkpoints/"]

[outputs]
paths = ["outputs", "checkpoints"]
```

Target selection follows `--target`, the current project's `loaf use` setting, then the TOML `target`. The personal selection never rewrites project TOML. Without `remote_dir`, each project receives a stable ID and a separate path under the target's remote base. Set that base with `loaf add ... --remote-dir '~/projects'`.

User configuration lives in `~/.config/loaf/config.toml`; project identities and personal selections live in `~/.local/state/loaf/index.json`. Private run locators are stored alongside it in `runs.json`; execution records and logs remain on the bound remote machine. `XDG_CONFIG_HOME` and `XDG_STATE_HOME` override these locations.

The current run index uses format 2. Previous indexes are rejected without migration or overwriting; use a separate `XDG_STATE_HOME` for v0.2 when retaining an earlier version's state.

To set personal sync defaults, add a `[sync]` table to the existing user configuration, keeping its `format_version` and machine entries:

```toml
[sync]
mode = "mirror"
respect_gitignore = true
exclude = ["scratch/"]
```

Personal `[run] wait = "10s"` and `[logs] tail = 100` defaults are also supported. Explicit flags override project preferences, then user preferences, then built-ins (`wait = "0s"`, `tail = 100`). Zero is an explicit override. Executable command, setup, and environment settings are project-only, never inherited from global configuration.

Project and user settings also support `[ps] limit = 20` and `[exec] cwd = ""`. List limits accept 1–1000, so an explicit zero is an error rather than a request for the default. An empty exec directory explicitly restores the remote home, even when a lower-priority setting selected another directory. Both settings use flag, project, user, then built-in precedence.

`run.wait` applies to submission only. `ps <run-id>` waits only when explicitly given `--wait`; list queries never wait for tasks to finish.

For sync strategies, explicit flags override project settings, then user defaults, then built-ins (`mirror` and `respect_gitignore = true`). Each scalar inherits independently; exclusions append across all scopes and never replace built-in safeguards. Setting `respect_gitignore = false` disables Git tracking/ignore selection, not Loaf's own exclusions. `update` leaves removed and newly excluded remote files in place and reports that warning in human output. JSON includes the effective `policy` and selection/exclusion results.

```bash
loaf sync --dry-run --mode update
loaf sync --respect-gitignore=false --exclude 'scratch/' --exclude '*.tmp'
loaf sync --json
```

## Acquire and connect AutoDL compute

Existing SSH targets need no cloud account. For AutoDL, log in with a main account's phone number or the full `subuser@master-id` login. One verified session can access **ordinary instances** (`standard`) and **Pro** (`pro`), subject to the account's permissions. Password input is hidden; credentials are never accepted as command-line flags.

```bash
loaf cloud login autodl-main --username 'your-subuser@master-id' --default
loaf cloud list --account autodl-main
loaf cloud list --account autodl-main --product pro
```

### Choose where to save credentials

```bash
loaf cloud login autodl-main --credential-store keyring   # Default
loaf cloud login autodl-main --credential-store file      # Unencrypted private files
```

`keyring` uses macOS Keychain or Linux Secret Service (requires a working user session). `file` needs no vault service: entries are stored under `$XDG_STATE_HOME/loaf/credentials/`, defaulting to `~/.local/state/loaf/credentials/`, with directory mode `0700` and file mode `0600`. File storage protects against other OS users, not software running as you. Keyring failures never silently fall back to files. The CLI supports macOS/Linux; Windows users currently need WSL.

Set a default for **new logins** in the user configuration, not project `loaf.toml`:

```toml
[cloud]
credential_store = "file"    # Or "keyring"
```

An existing account keeps its saved store unless `--credential-store` explicitly changes it. A switch or account replacement retains earlier credential entries for historical run bindings; it does not rewrite their access. `cloud logout <name>` deletes only the currently selected entry. `cloud account remove <name>` removes only the alias/default, not credentials or cloud resources. Revoke sessions on the platform to invalidate all copies.

Loaf saves the session token and, by default, AutoDL's replayable SHA-1 password digest—not the original password. **The digest is a password equivalent**, including in file mode. `--remember-password=false` saves only the session. When it expires, `cloud login autodl-main` reauthenticates using the saved digest; `--prompt` forces fresh password entry. A changed verified principal requires `--replace` and receives a new credential reference. Failed authentication leaves the existing saved login intact.

Main-account SMS/CAPTCHA challenges return `AUTH_CHALLENGE_REQUIRED`; complete verification through AutoDL, then use `cloud login autodl-main --verify` for an obtained SMS code. Loaf does not solve CAPTCHA or automatically replay password requests. Noninteractive callers may use `--username ... --password-stdin`; `--json` never prompts. Do not put passwords in shell history or chat.

For automation, explicit environment-token references remain available:

```bash
loaf cloud account add autodl-pro --product pro --auth developer \
  --token-env AUTODL_PRO_TOKEN
loaf cloud account add autodl-session --product standard --auth session \
  --token-env AUTODL_WEB_TOKEN
loaf cloud account list --json
```

Tokens are read from the calling environment; TOML contains only their variable names. Developer tokens use the official Pro control plane and cannot access ordinary instances. Session tokens use the web control plane; saved native logins verify main/sub identity and permissions. `--app-version` can explicitly supply the web console header. No token scraping or automatic developer-token creation is performed.

`cloud login --product pro` sets an account's default product. Per operation, `--product` overrides the resource profile's `product`, then that default. Bound aliases keep their saved product and reject conflicting overrides. Authentication mode and product selection never silently switch API hosts or credentials.

### Connect an existing instance

```bash
loaf cloud credentials <instance-id> --account autodl-main
loaf cloud bind gpu-a --instance <instance-id> --account autodl-main
loaf use gpu-a
loaf run -- python3 train.py
```

`credentials` shows the current SSH endpoint, redacting its password. Only explicit `--show-password` reveals it. Establish first-host trust with native SSH; noninteractive commands never approve a new or changed host key. Cloud connections prefer keys and can use a fetched password through native OpenSSH askpass. Passwords stay in memory behind a temporary private local socket, not in configuration, subprocess arguments or environment variables. The short-lived launcher contains no password. Cloud SSH connections do not reuse a pre-existing multiplexed master; static SSH targets retain their original behavior.

`bind` checks that the account can inspect this instance, saves the binding, then verifies authenticated SSH. `ready` means platform running + SSH authenticated + target saved, not Python/CUDA ready. `--ready-timeout` defaults to 2m (0s makes one bounded check). A created but unreachable instance may already be billing: keep its ID and fix its connection, rather than renting another. Retry a saved acquisition with `loaf cloud bind gpu-a --request <request-id>`; it never creates a replacement. An explicit `--replace` can rebind an alias; older runs still refresh their original instance's endpoint.

### Choose conditions in configuration or directly

An optional project `loaf.toml` profile describes the experiment's resource needs, not credentials:

```toml
[resources.train]
gpu_vendor = "nvidia"
gpu_count = 1
min_gpu_memory_gib = 24       # Per card, not the total across GPUs
min_memory_gib = 32
max_compute_hourly_price = "2.50"
currency = "CNY"
wait_timeout = "30m"
poll_interval = "10s"

[resources.train.autodl]
image = "your-image-uuid"
```

Ordinary and session-backed Pro requests use the platform's price-preview API. Budgets use exact decimal CNY and the **whole instance's hourly compute fee**; separately quoted storage is daily. Loaf does not guess units from raw catalog prices or require users to declare them. A new quote is checked immediately before creation. An hourly cap is neither a total-spend limit nor a server-enforced price lock; the platform can still change between quote and allocation.

```bash
loaf cloud search --account autodl-main --profile train
loaf cloud watch --account autodl-main --profile train
loaf cloud acquire gpu-a --account autodl-main --profile train --yes
loaf use gpu-a
loaf run -- python3 train.py
loaf logs
loaf pull
```

No profile is required:

```bash
loaf cloud acquire gpu-a --account autodl-main \
  --gpu-count 1 --min-gpu-memory 24GiB --min-memory 32GiB \
  --max-compute-hourly-price 2.50 --currency CNY \
  --image your-image-uuid --wait 30m --yes
```

Other conditions are `--gpu-vendor`, `--gpu-model`, `--min-cpus`, `--min-disk` and `--region`. Models and regions are exact platform labels/IDs; inspect search output rather than guessing them. Memory/disk CLI floors require the `GiB` suffix. Ordinary data-disk expansion uses `--expand-data-disk`; Pro system expansion uses `--expand-system-disk` (both use the platform's GB convention). Image pullability and dependency readiness are not verified before rental. Image settings are product-specific: `--image` selects an ordinary base-image UUID or a Pro image UUID. For a private image with either web-session product, use `--private-image` instead of `--image`, not both. Developer Pro uses its own image UUID.

Precedence is explicit flags, project profile, user profile, then defaults, **independently per field**. `resources.default` supplies optional shared defaults; a named profile overlays it. User `[cloud.defaults]` supplies lower-priority personal defaults. Model/region lists replace the whole lower-priority list, rather than widening it. Explicit zero/empty overrides are preserved when valid. Accounts and `cloud.default_account` are user-only; project files cannot contain them.

A separate user safety ceiling cannot be raised or cleared by project settings or CLI flags:

```toml
[cloud.safety]
max_compute_hourly_price = "3.00"
currency = "CNY"
```

### Monitoring is not a paid allocation

| Operation | Behavior |
| --- | --- |
| `cloud search` | One read-only bounded inventory query; filters unknown required fields out and ranks eligible offers by price. |
| `cloud watch` | Foreground read-only monitoring; requires a positive `--wait`/profile timeout. Never rents. |
| `cloud acquire <alias>` | Explicit authorization for at most one instance; rechecks conditions, records intent before creation, then connects it. |

Polling cycles start at 10s, respect a configurable 10s–10m minimum (creation adds one immediate recheck), and add backoff/jitter and `Retry-After` on transient failures. Waiting is bounded to 24h; closing the client ends monitoring, not a resource already created. Human output reports matches, rejection reasons, coverage and observation time. JSON finite commands/acquire return one envelope; `watch --json` uses NDJSON change events, sparse heartbeats and a final end/error. Changed configuration cannot broaden an already running authorization.

Ordinary inventory is an **unofficial web-session API**. Hardware units are normalized from the platform's public rental form: GPU memory is per card; CPU/RAM allocations scale by card count. Offline machines and exhausted instance slots are excluded. Missing required fields stay unknown. The adapter examines at most `--limit` entries (100 by default, up to 1000); incomplete pagination remains visible and “cheapest” means cheapest within that examined set.

Session-backed Pro supports `search` and `watch` using GPU catalogs, numeric spec IDs and per-request quotes:

```bash
loaf cloud search --account autodl-main --product pro --region westDC3
loaf cloud acquire pro-a --account autodl-main --product pro \
  --gpu-model 'platform-model-name' --region westDC3 \
  --image your-base-image-uuid --cuda-from 118 \
  --max-compute-hourly-price 3.00 --wait 10m --yes
```

Without `--region`, web Pro searches the console's supported `westDC3` and `beijingDC2` data centers separately. `--gpu-spec-id` optionally fixes a numeric web spec; otherwise matching selects it. It is distinct from developer `--gpu-spec` (UUID). Catalog stock is aggregate model/region availability, not reserved capacity for a spec. CPU/RAM ranges are conservative floors, not promised final allocations; fields the catalog does not verify, including GPU memory/vendor, remain unknown and cannot satisfy a requested floor. The selected region/model/spec is rechecked, and allocation can still return no stock. Neither product substitutes Elastic inventory for Pro.

Pro's developer API supports create/list/info/start/stop/connection, but has no verified read-only stock or pre-allocation price capability in this adapter. `search`/`watch` return `UNSUPPORTED_CAPABILITY`. An explicit Pro spec can instead use **authorized allocation attempts**, retrying only a narrowly mapped, confirmed no-stock rejection:

```bash
loaf cloud acquire pro-a --account autodl-pro \
  --gpu-spec your-spec-uuid --image your-image-uuid --cuda-from 118 \
  --gpu-count 1 --wait 10m --allow-unquoted --yes
```

This is not read-only stock monitoring. `--allow-unquoted` cannot bypass an effective price ceiling; that case returns `PRICE_UNVERIFIABLE` without charging. Unverifiable generic hardware floors are also refused, not silently relaxed. Pro region IDs are data-center IDs, unlike ordinary region-sign IDs. `118` is the platform's CUDA 11.8 lower-bound code. Loaf never uses a platform startup command to run experiments. See the [official Pro API](https://www.autodl.com/docs/instance_pro_api/), the [current ordinary rental form](https://www.autodl.com/assets/index.9040cb9d.js), and the [GET-query observation](https://github.com/RordChang/autodl-pro-codex-skill/blob/main/references/api.md). Developer Pro snapshot/status use query parameters; no automatic GET-body fallback or mutation replay is attempted.

### Recover uncertainty and control billing explicitly

```bash
loaf cloud requests --json
loaf cloud requests <request-id> --json
loaf cloud reconcile <request-id> --instance <known-instance-id> --json
loaf cloud bind gpu-a --request <request-id>
loaf cloud start gpu-a --wait 2m --yes
loaf cloud stop gpu-a --wait 2m --yes
```

Receipts are private JSON files beside the run index under `cloud/`. Network loss, cancellation during creation, invalid responses and ambiguous server failures return `CLOUD_UNCONFIRMED` (exit 3), retaining the request ID and any known instance ID. Loaf never retries such a mutation or automatically rents a replacement; unresolved allocation intents block further acquisitions on that account. Only confirmed mapped no-stock failures can trigger another sequential attempt within the original approval window.

For an unknown creation, check the platform console and reconcile its known ID. If a human has verified that **no instance was created**, `cloud reconcile <request-id> --no-instance --yes` clears the guard. This assertion is not an automatic inference from a bounded list. Power-operation receipts reconcile by querying the same saved instance; reconciliation never repeats a power request.

`cloud start/stop` affect the **entire instance**, unlike `loaf stop`, which stops one experiment. An accepted power request is not completion; use `--wait` or `info` to observe it. Stopping an instance can affect other users/jobs, and storage may keep billing afterward. Loaf does not release instances, shut them down after training, or impose a runtime/spend limit. Renting does not start training, and `run` never rents implicitly.

## Languages and structured output

Help, prompts, errors, and check output support English and Simplified Chinese:

```bash
loaf --lang zh-Hans --help
LOAF_LANG=en loaf check --json
```

Language precedence is `--lang`, `LOAF_LANG`, then `language` in the user configuration. The default `auto` uses the system locale. Language preference is not a shared project setting.

Core commands support `--json` with `schema_version: 1`, a stable `ok` field, and structured data or an error code. Human-readable `message` and `hint` can change language; command names, keys, and error codes do not. External diagnostics retain their original text. Operation exit codes are 0 for a successful operation, 2 for arguments/configuration, 3 for unconfirmed submission, stopping, foreground execution, cleanup, or cloud mutation, and 1 for other failures. The training exit code belongs to `data.exit_code`, not the CLI exit status. A successful query of failed training has `ok: true` and `data.status: "failed"`. Nonterminal tasks have no training exit code; unknown outcomes are reported honestly.

## Troubleshooting

For missing source files, use `loaf sync --dry-run` to inspect Git and Loaf exclusions. A project inside a parent repository's ignored directory remains subject to that ignore rule. For a failed command, inspect `loaf logs`; an accepted run can still fail because an interpreter, dependency, or input is missing. For a lost submission or stop confirmation, query the saved ID instead of starting a replacement task.

## Development

```bash
go run ./scripts/format.go    # Four-space Go formatting
bash scripts/check.sh        # Unit, static, race, format, and vulnerability checks
bash scripts/integration.sh  # Real SSH and Linux process tests; requires Docker
bash scripts/integration.sh -run TestProjectRunWaitSetupAndRecovery
```

The integration fixture uses temporary keys and CPU scripts. It tests packaged macOS/host and Linux clients with native rsync, mirror boundaries, interrupted sync, concurrent starts, matching runner deployment, jump hosts, disconnected execution, saved exits, process-group stopping, bounded waits, log cancellation, and incremental downloads. Foreground operations cover live output, literal argv, PTY login, isolated tunnels, and cancellation. History and cleanup cases exercise saved target bindings, explicit deletion scope, transfer locks, retry, and retained terminal outcomes without touching research servers. Build targets are macOS/Linux clients and Linux amd64/arm64 runners; cross-build results and runtime tests are separate checks.

Cloud tests use simulated APIs for both AutoDL products and isolated SSH servers, including native password authentication, rejected unknown host keys, immutable instance binding and result downloads. These tests do not establish real-account API availability, current stock, or actual billing. Real rental acceptance requires explicit account and budget authorization.

Loaf does not schedule GPU workloads, automatically repair environments, or replace experiment tracking. Cloud acquisition is explicit; there are no queues, daemon, automatic shutdown or implicit rentals.

Maintainers publish by explicitly pushing a `vX.Y.Z` or prerelease tag. The release workflow runs the same CI and SSH checks, calls `scripts/build.sh`, attests the archives, and creates the release. Ordinary branches and pull requests have no publishing permissions.

## License

[MIT](LICENSE)
