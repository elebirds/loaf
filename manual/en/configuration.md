# Configure a project and personal defaults

[Home](../../README.md) · [Command reference](commands.md) · [简体中文](../zh-Hans/configuration.md)

The task-list command is `status`; its existing configuration section remains `[ps]`.

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

Target selection follows `--target`, the current project's `loaf machine use` setting, then the TOML `target`. The personal selection never rewrites project TOML. Without `remote_dir`, each project receives a stable ID and a separate path under the target's remote base. Set that base with `loaf machine add ... --remote-dir '~/projects'`.

User configuration lives in `~/.config/loaf/config.toml`. Project identities, personal selections, run locators, labels, observations, cleanup summaries and cloud receipts share `~/.local/state/loaf/state-v3/state.db`. `XDG_CONFIG_HOME` and `XDG_STATE_HOME` override the configuration/state bases. Credentials stay in the selected keyring or the separate private `credentials/` directory.

Native Windows defaults to `%APPDATA%\loaf\config.toml` and `%LOCALAPPDATA%\loaf\state-v3\state.db`. XDG overrides take precedence and append `loaf` to the base. Configuration, state, credentials and locks use private Windows ACLs; existing unsafe permissions are rejected. Keep WSL state and credentials in its own Linux home, separate from Windows. `LOAF_MSYS2_ROOT` selects the paired native transport installation; see [installation](installation.md).

See [upgrading](upgrading.md) for the v0.6 state boundary and matching old CLIs.

Each new remote project keeps its schema-2 control database in `<remote-project>/.loaf-control-v2/state.sqlite3`; sources, per-run working copies, logs and outputs remain ordinary files. Both databases use private permissions, WAL, FULL synchronization and bounded lock waits. Unknown schemas fail explicitly. Software, CLI schema, wire record and database schema versions are independent.

To set personal sync defaults, add a `[sync]` table to the existing user configuration, keeping its `format_version` and machine entries:

```toml
[sync]
mode = "mirror"
respect_gitignore = true
exclude = ["scratch/"]
```

Personal `[run] wait = "10s"` and `[logs] tail = 100` defaults are also supported. Explicit flags override project preferences, then user preferences, then built-ins (`wait = "0s"`, `tail = 100`). Zero is an explicit override. Executable command, setup, and environment settings are project-only, never inherited from global configuration.

Project and user settings also support `[ps] limit = 20` and `[exec] cwd = ""`. List limits accept 1–1000, so an explicit zero is an error rather than a request for the default. An empty exec directory explicitly restores the remote home, even when a lower-priority setting selected another directory. Both settings use flag, project, user, then built-in precedence.

`run.wait` applies to submission only. `status <run-id>` waits only when explicitly given `--wait`; list queries never wait for tasks to finish.

For sync strategies, explicit flags override project settings, then user defaults, then built-ins (`mirror` and `respect_gitignore = true`). Each scalar inherits independently; exclusions append across all scopes and never replace built-in safeguards. Setting `respect_gitignore = false` disables Git tracking/ignore selection, not Loaf's own exclusions. `update` leaves removed and newly excluded remote files in place and reports that warning in human output. JSON includes the effective `policy` and selection/exclusion results.

```bash
loaf sync --dry-run --mode update
loaf sync --respect-gitignore=false --exclude 'scratch/' --exclude '*.tmp'
loaf sync --json
```
