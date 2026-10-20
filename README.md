# Loaf

[English](README.md) | [简体中文](README.zh-Hans.md)

**Write code locally. Use your existing SSH research machines.**

Loaf keeps remote machine addresses, project selections, and environment checks close to your code. Reuse your SSH configuration, keys, and jump hosts without handing credentials to another service.

> Early development: `add`, `use`, `check`, and `version` are available from source. Source sync, the background-run CLI, logs, and result downloads are still being built.

## Build from source

Requires Go 1.27.1, Git, OpenSSH, and rsync. The Go version is pinned in `mise.toml`; review that file before trusting it if you use mise.

```bash
bash scripts/build.sh
export PATH="$PWD/dist:$PATH"
loaf --help
```

The build produces a local executable plus a compressed archive in `dist/`. There is no installable release yet. You can also place `dist/loaf` in a directory already on your PATH.

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

Python, uv, and GPU tools are optional observations. Their absence does not make a CPU-capable machine fail the check. Missing rsync, unsupported platforms, or an unwritable remote location do.

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

The integration fixture uses temporary keys and verifies packaged connection checks, jump hosts, and spaced SSH configuration paths without touching research servers.

Loaf does not provision machines, schedule GPUs, automatically repair environments, or replace experiment tracking.

## License

[MIT](LICENSE)
