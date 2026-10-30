# Build and develop Loaf

[Home](../../README.md) · [Command reference](commands.md) · [简体中文](../zh-Hans/development.md)

## Architecture

Loaf is a modular monolith with two executables, not a required daemon or API server:

```text
loaf: Kong declarations → command adapters / rendering
                        → client.Projects / Machines / Resources
                        → run / cloud / source selection
                        → OpenSSH / rsync / AutoDL / state / credentials
                                      │
loaf-runner: short control request ← SSH / rsync
          → managed project + one Supervisor per accepted run
          → project SQLite control state + ordinary payload files
```

The CLI is the shared workflow for people and agents. Only GPU acquisition has
a multi-step TUI; login, machine registration and destructive actions use local
input or confirmation. There is no root dashboard or cross-command navigation.
Configuration is a command-local snapshot; a resource workflow refreshes its
own settings after an explicit account/configuration change.

### Responsibilities and dependencies

- `cmd/loaf` composes concrete dependencies and process cancellation. `internal/cli` converts declared arguments to typed requests and renders finite results, NDJSON or native streams. Huh handles ordinary prompts and default-no confirmations; login uses one bounded native input session for username, password and verification. Forms and progress use stderr; JSON never prompts.
- `internal/client` owns complete local workflows. **Projects** plans/submits/observes runs and coordinates transfer/cleanup; **Machines** manages aliases, checks and foreground operations; **Resources** handles resource selection, authorization, acquisition, binding and authentication. A caller supplies explicit authentication policy and progress sinks, not a parser, localizer or output-mode switch. `App` only composes these services.
- `internal/run` owns execution semantics and recovery. Its pure `ResolveExecution` function normalizes and validates command, setup, environment and output selection for both preview and submission; previews expose environment names only. `internal/cloud` owns resource conditions, lifecycle intents and small provider capabilities; `internal/sync` owns selection and transfer rules. Consumer-owned interfaces isolate actual side effects. There is no generic repository, backend registry or DI container.
- `internal/ssh`, `internal/autodl` and `internal/credentials` implement external boundaries. SSH commands and quoting stay at the transport boundary, preserving the system OpenSSH and rsync protocols.
- `internal/task` holds frozen run locators, navigation labels and public observations. `internal/config` reads TOML, discovers projects, merges preferences and validates values. `internal/state/local`, `internal/state/receipts` and `internal/state/remote` perform explicit SQL through `database/sql` and `modernc.org/sqlite`, without an ORM. Local state records intent and location; remote state records authoritative execution facts.
- `internal/remote` owns managed directories, process identity, group termination, reaping and final publication. It depends on wire types and system capabilities, not CLI, local configuration or cloud accounts. `cmd/loaf-runner` dispatches control requests or starts a per-run Supervisor.

### Submission, uncertainty and cleanup

The application resolves a plan, checks prerequisites, and saves the run identity and frozen binding as `preparing`. Source synchronization then receives the working files; the runner exclusively claims that ID and prepares its independent work copy. The client saves `uncertain` before attempting one launch, then records confirmed acceptance as `submitted`. A lost response leads to observation of **the same ID**, not another launch with a new ID. Accepted submission, observed execution state, observation failure and local-save failure remain distinct results. Canceling a wait does not stop an accepted run.

SQL transactions cover short metadata updates only; they never span SSH, HTTP, file copying or process runtime. Existing OS locks still govern source receive/copy, lifecycle admission, process lifetime and payload access. Cleanup removes only the selected terminal run payload and private execution data, retaining its claim and public deletion summary so a delayed launch cannot resurrect it. Local cleanup availability is separate from execution outcome.

## Product boundaries

Keep direct execution simple: run already prepares, synchronizes and submits.
The GPU form and flags share the same Resources workflow. Put new behavior in
its owning use case, not in menu navigation or a second command model.
Test the changed path and its concrete side effects; reuse the existing shared
checks and isolated integration entry point instead of adding another gate.

## Development

```bash
go run ./scripts/format.go    # Four-space Go formatting
go tool depguard ./...       # Package dependency boundaries
bash scripts/check.sh        # Format, dependency, unit, static, race and vulnerability checks
bash scripts/integration.sh  # SSH, Linux process and credential tests; requires Docker
bash scripts/integration.sh -run TestProjectRunWaitSetupAndRecovery
bash scripts/integration.sh -run TestLinuxCloudCredentialStores
```

The shared check script runs the pinned standalone depguard tool after formatting checks. Its deny rules protect the CLI/use-case/remote boundaries without imposing an allowlist on every package.

The integration fixture uses temporary keys and CPU scripts. It tests packaged macOS/host and Linux clients with native rsync, mirror boundaries, interrupted sync, concurrent starts, matching runner deployment, jump hosts, disconnected execution, saved exits, process-group stopping, bounded waits, log cancellation, and incremental downloads. Foreground operations cover live output, literal argv, PTY login, isolated tunnels, and cancellation. History and cleanup cases exercise saved target bindings, explicit deletion scope, transfer locks, retry, and retained terminal outcomes without touching research servers. Build targets are macOS/Linux clients, a Windows x64 ZIP client, and Linux amd64/arm64 runners; cross-build results and runtime tests are separate checks. Native Windows uses paired MSYS2 OpenSSH/rsync and independent connections. Linux and WSL2 retain private connection reuse. Local fault injection uses Go subprocess helpers; controlling-terminal tests use PTYs on Unix and owned ConPTYs on Windows. Windows unit tests exercise private ACLs, inherited WAL protection, cross-process locks, failed replacement, database recovery, secret-input cancellation and Job Object ownership. Set `LOAF_WINDOWS_TEST_VOLUME` to a second local NTFS drive to include cross-drive downloads. In WSL2, set `LOAF_WSL_PROJECT_BASE` to an existing test directory under `/mnt/c` to verify a mounted project with separate Linux private state. Windows 10 runtime acceptance remains pending until an independent test environment is available.

Cloud tests use simulated APIs for both AutoDL products and isolated SSH servers, including native password authentication, rejected unknown host keys, immutable instance binding and result downloads. Fresh-process tests cover expired sessions, saved-digest reauthentication, changed passwords, logout/login, credential-store selection, and recovery after a lost response or killed client without duplicate acquisition or power requests.

The Linux credential fixture runs an unprivileged Secret Service keyring in a private container session, exercising native vault access, the file-to-keyring login lifecycle, and an unavailable session bus. It never mounts the host keyring. Platform HTTP remains simulated; these tests do not establish real-account API availability, current stock, or actual billing. Real rental acceptance requires explicit account and budget authorization.

The optional single-target FIFO queue uses an explicitly enabled remote Coordinator; direct runs do not require it. Loaf does not add experiment retries, preemption, multi-node scheduling, automatic environment repair or automatic cloud shutdown. Cloud acquisition remains explicit.

Maintainers publish by explicitly pushing a `vX.Y.Z` or prerelease tag. The release workflow runs the same CI and SSH checks, calls `scripts/build.sh`, attests the archives, and creates the release. Ordinary branches and pull requests have no publishing permissions.
