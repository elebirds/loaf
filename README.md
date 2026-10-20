# Loaf

[English](README.md) | [简体中文](README.zh-Hans.md)

**Write code locally. Use your existing SSH research machines.**

Loaf synchronizes working code to your SSH machines and runs experiments that survive disconnection, with saved task IDs, logs, and exit results. Reuse your SSH configuration, keys, and jump hosts without handing credentials to another service.

> Early development: `add`, `use`, `check`, `sync`, `run`, `ps`, `logs`, and `version` are available from source. Public stop and output-download commands are not available yet.

## Build from source

Requires Go 1.27.1, Git, OpenSSH, and rsync. The Go version is pinned in `mise.toml`; review that file before trusting it if you use mise.

```bash
bash scripts/build.sh
export PATH="$PWD/dist:$PATH"
loaf --help
```

The build embeds Linux amd64 and arm64 runners and produces a local executable plus a compressed archive with the Agent Skill and examples in `dist/`. There is no installable release yet. You can also place `dist/loaf` in a directory already on your PATH.

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

`add` saves the connection locally; `check` actually connects. JSON and non-TTY checks do not ask for passwords or host trust. Establish access with native SSH first when needed. Loaf never saves passwords or disables host-key verification.

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

`ps` lists the latest 20 submitted or submission-uncertain runs in this project (`--limit` changes the bound). An ID queries that run only. Failed queries preserve successful rows and do not label training as failed. `logs` without an ID selects the latest submitted or submission-uncertain run, not an older fallback. Proven preflight/sync failures do not replace it. Saved runs keep their original connection, remote location, and runner version when project defaults change; `--target` cannot redirect queries.

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

Loaf does not install Python or uv, guess environment activation, or upload datasets. Provision those explicitly. Data/shared environments can live beside `cache/`; refer to them by absolute paths because execution happens in `<remote_dir>/runs/<run-id>/work/`. Outputs currently remain there until collected with native tools. Output paths are frozen at submission for the future download command.

Try the [dependency-free CPU example](examples/cpu/) or the [explicit setup example](examples/setup/). Agents can use the short [Loaf Skill](skills/loaf/SKILL.md); they do not need to inspect internal files, PIDs, or locks.

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
- Loaf additionally excludes virtual environments, common caches, `.git`, `.env`/`.env.*`, common private-key names, `*.pem`, `*.key`, `*.pt`, `*.pth`, `*.ckpt`, `*.safetensors`, and `loaf-results/`, even when tracked. Configured output paths are excluded; the default is `outputs/`.
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

To set personal sync defaults, add a `[sync]` table to the existing user configuration, keeping its `format_version` and machine entries:

```toml
[sync]
mode = "mirror"
respect_gitignore = true
exclude = ["scratch/"]
```

Personal `[run] wait = "10s"` and `[logs] tail = 100` defaults are also supported. Explicit flags override project preferences, then user preferences, then built-ins (`wait = "0s"`, `tail = 100`). Zero is an explicit override. Executable command, setup, and environment settings are project-only, never inherited from global configuration.

`run.wait` applies to submission only. `ps <run-id>` waits only when explicitly given `--wait`; list queries never wait for tasks to finish.

For sync strategies, explicit flags override project settings, then user defaults, then built-ins (`mirror` and `respect_gitignore = true`). Each scalar inherits independently; exclusions append across all scopes and never replace built-in safeguards. Setting `respect_gitignore = false` disables Git tracking/ignore selection, not Loaf's own exclusions. `update` leaves removed and newly excluded remote files in place and reports that warning in human output. JSON includes the effective `policy` and selection/exclusion results.

```bash
loaf sync --dry-run --mode update
loaf sync --respect-gitignore=false --exclude 'scratch/' --exclude '*.tmp'
loaf sync --json
```

## Languages and structured output

Help, prompts, errors, and check output support English and Simplified Chinese:

```bash
loaf --lang zh-Hans --help
LOAF_LANG=en loaf check --json
```

Language precedence is `--lang`, `LOAF_LANG`, then `language` in the user configuration. The default `auto` uses the system locale. Language preference is not a shared project setting.

Core commands support `--json` with `schema_version: 1`, a stable `ok` field, and structured data or an error code. Human-readable `message` and `hint` can change language; command names, keys, and error codes do not. External diagnostics retain their original text. Operation exit codes are 0 for a successful operation, 2 for arguments/configuration, 3 for unconfirmed submission, and 1 for other failures. The training exit code belongs to `data.exit_code`, not the CLI exit status. A successful query of failed training has `ok: true` and `data.status: "failed"`. Nonterminal tasks have no training exit code; unknown outcomes are reported honestly.

## Development

```bash
go run ./scripts/format.go    # Four-space Go formatting
bash scripts/check.sh        # Unit, static, race, format, and vulnerability checks
bash scripts/integration.sh  # Real SSH and Linux process tests; requires Docker
bash scripts/integration.sh -run TestProjectRunWaitSetupAndRecovery
```

The integration fixture uses temporary keys and CPU scripts. It tests packaged macOS/host and Linux clients with native rsync, policy overrides, mirror deletion boundaries, interrupted-sync recovery, concurrent starts, runner deployment, jump hosts, disconnected execution, saved exits, duplicate submissions, stopping child processes, bounded waits, setup failures, log cancellation, and lost-confirmation recovery without touching research servers. Build targets are macOS/Linux clients and Linux amd64/arm64 runners; cross-build results and runtime tests are separate checks.

Loaf does not provision machines, schedule GPUs, automatically repair environments, or replace experiment tracking.

## License

[MIT](LICENSE)
