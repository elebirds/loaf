# Loaf

[English](README.md) | [简体中文](README.zh-Hans.md)

**Write code locally. Use your existing SSH research machines.**

Loaf keeps remote machine addresses, project selections, and environment checks close to your code. Reuse your SSH configuration, keys, and jump hosts without handing credentials to another service.

> Early development: `add`, `use`, `check`, and `version` are available from source. Local source previews are available with sync --dry-run. Remote source transfer, the background-run CLI, logs, and result downloads are still being built.

## Build from source

Requires Go 1.27.1, Git, OpenSSH, and rsync. The Go version is pinned in `mise.toml`; review that file before trusting it if you use mise.

```bash
bash scripts/build.sh
export PATH="$PWD/dist:$PATH"
loaf --help
```

The build embeds Linux amd64 and arm64 runners and produces a local executable plus a compressed archive in `dist/`. There is no installable release yet. You can also place `dist/loaf` in a directory already on your PATH.

## Set up a research machine

From your research project directory:

```bash
# Register a platform-provided SSH connection
loaf add autodl --ssh "ssh -p 12345 root@example.com"
loaf use autodl

# Check the connection, remote directory, tools, and disk
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

## Preview working sources

```bash
loaf sync --dry-run
loaf sync --dry-run --mode update --respect-gitignore=false --exclude 'scratch/'
loaf sync --dry-run --json
```

This command selects local working files and reports policy and exclusions. It requires no target, connection, or runner and creates no project state. File transfer is not available yet; --dry-run is required.

Git projects include tracked working files and nonignored new files, preserving nested ignore and negation rules. Non-Git projects read nested .gitignore files. Loaf additionally excludes virtual environments, caches, weights, private-key names, .env files, and output paths, even when tracked. Safe relative links to selected paths are preserved; absolute, escaping, broken, or excluded-target links are reported and skipped.

Optional project and user [sync] settings support mode = "mirror" or "update", respect_gitignore = true or false, and exclude = ["data/"]. Flags override project settings, then user settings, then built-ins (mirror and true); each scalar inherits separately. Exclusions append and cannot negate built-in exclusions. The preview displays the effective policy. Update mode retains removed/newly excluded remote files when transfer is added; mirror selects exact source-set convergence.

## Project configuration

Projects are detected from the nearest `loaf.toml`, then the Git worktree root, then the current directory. No `init` is required. An optional project file can select a machine and remote directory:

```toml
target = "autodl"
remote_dir = "~/projects/my-research"
```

Target selection follows `--target`, the current project's `loaf use` setting, then the TOML `target`. The personal selection never rewrites project TOML. Without `remote_dir`, each project receives a stable ID and a separate path under the target's remote base. Set that base with `loaf add ... --remote-dir '~/projects'`.

User configuration lives in `~/.config/loaf/config.toml`; project identities and personal selections live in `~/.local/state/loaf/index.json`. `XDG_CONFIG_HOME` and `XDG_STATE_HOME` override these locations.

## Languages and structured output

Help, prompts, errors, and check output support English and Simplified Chinese:

```bash
loaf --lang zh-Hans --help
LOAF_LANG=en loaf check --json
```

Language precedence is `--lang`, `LOAF_LANG`, then `language` in the user configuration. The default `auto` uses the system locale. Language preference is not a shared project setting.

Core commands support `--json` with `schema_version: 1`, a stable `ok` field, and structured data or an error code. Human-readable `message` and `hint` can change language; command names, keys, and error codes do not. External diagnostics retain their original text. Operation exit codes are 0 for success, 2 for arguments/configuration, and 1 for connection or operation failures.

## Development

```bash
go run ./scripts/format.go    # Four-space Go formatting
bash scripts/check.sh        # Unit, static, race, format, and vulnerability checks
bash scripts/integration.sh  # Real SSH and Linux process tests; requires Docker
```

The integration fixture uses temporary keys and CPU scripts. It tests packaged runner deployment, jump hosts, disconnected execution, saved exits, duplicate submissions, and stopping child processes without touching research servers. Build targets are macOS/Linux clients and Linux amd64/arm64 runners; cross-build results and runtime tests are separate checks.

Loaf does not provision machines, schedule GPUs, automatically repair environments, or replace experiment tracking.

## License

[MIT](LICENSE)
