# Agent guide for Loaf

Loaf is a Go CLI for running local research projects on existing SSH machines. The core workflow is connect, sync, run in the background, inspect logs, stop, and retrieve outputs. Environment checks, English and Simplified Chinese, and structured JSON output belong to the first release.

Keep machine provisioning, GPU scheduling, queues, automatic retries, central services, Web UI, and MCP out of scope. Interactive SSH, ad hoc execution, and port forwarding follow the core workflow.

## Start here

1. Inspect `git status --short` and the existing diff. Preserve unrelated and untracked work; do not reset or clean the checkout.
2. Read the relevant README sections, the target code, its callers, and nearby tests. Read any more-specific `AGENTS.md` before editing that subtree.
3. Read `go.mod`, `mise.toml`, and existing scripts when present. Verify tools with `command -v` before diagnosing a missing runtime; use the project's declared versions through PATH and mise, not an assumed system installation. Review a mise configuration before trusting it.
4. Establish the behavior being changed and the smallest check that demonstrates it. Keep the implementation, affected tests, and user-facing examples in the same change.

## Repository map

Use this layout as implementation lands; do not create empty packages to fill the map.

```text
cmd/loaf/          Local CLI entry point and dependency composition
cmd/loaf-runner/   Linux runner entry point
internal/cli/     Commands, flags, interaction, human and JSON output
internal/i18n/    Locale selection and embedded message catalogs
internal/config/  TOML, project discovery, concrete local index storage
internal/ssh/     OpenSSH processes, connection arguments, remote quoting
internal/sync/    Source selection, rsync orchestration, output downloads
internal/run/     Local run, status, logs, and stop use cases
internal/remote/  Linux receiver, supervisor, process control, run records
internal/wire/    Shared versioned request and response types
internal/bundle/  Runner embedding and version metadata
internal/atomicfile/  Durable private file replacement
internal/fault/   Stable error codes and message identifiers
tests/integration/  Isolated Linux SSH fixtures and workflow tests
examples/         Minimal CPU and explicit setup examples
scripts/          Shared build, check, and integration entry points
skills/loaf/      Instructions for Agents using the CLI
```

Keep command handlers thin. Compose dependencies at the entry point with constructors; use consumer-owned interfaces for remote control, source sync, and the run index. Plain values and pure helpers do not need interfaces. Do not introduce a DI container, generic Repository, Provider framework, or backend registry without an actual requirement.

`run` and `remote` must not depend on CLI rendering or i18n. `remote` depends on shared wire types and system capabilities, not local configuration, Cobra, the SSH client, or `bundle`. Keep Go support packages internal; this release does not expose a Go SDK.

## Build and development commands

Run from the repository root. Commands requiring Go sources, a module, or a script apply only when those inputs exist. If `go.mod` is absent, there is no Go build/test entry point yet; do not bootstrap one for a prose-only task.

| Task | Entry point | Requirement |
| --- | --- | --- |
| Format Go changes | `go run ./scripts/format.go` | Standard Go AST formatting with four-space indentation; use `-check` in CI |
| Test one package or case | `go test ./internal/run -run TestName` | Example: replace the package and test selector with the affected ones |
| Run ordinary tests | `go test ./...` | Go module and source present |
| Static checks | `go vet ./...` | Go module and source present |
| Called-dependency vulnerabilities | `go tool govulncheck ./...` | Tool version is pinned in go.mod; requires access to the public vulnerability database |
| Check concurrency changes | `go test -race ./...` | A supported local or CI runtime; Linux process cases still require Linux |
| Build the complete CLI | `bash scripts/build.sh` | Add this working script with the first implementation |
| Run shared checks | `bash scripts/check.sh` | Use once implemented; local and CI checks share this entry point |
| Run SSH integration tests | `bash scripts/integration.sh` | Use once implemented with the isolated fixture and a Docker-compatible engine |
| Check prose changes | `git diff --check`, then inspect links and examples | Inspect new untracked files as well; Go tests are not needed |

Start with affected-package tests. For Go changes, follow with ordinary tests and vet; use race, integration, and packaging checks when the changed behavior calls for them. Report an unavailable check and its effect instead of substituting a passing unrelated command.

The complete build has one owner: `scripts/build.sh` builds Linux amd64/arm64 runners, writes generated assets under `internal/bundle`, then builds the client with embedded assets. A mise build task, CI, and release jobs call this same script. Do not add parallel Make, Task, or release-tool build paths.

Build runners without the client embedding tag. Separate embedded and development implementations so a clean checkout can run ordinary tests without generated binaries. A development build lacking a runner must give an actionable build instruction, not download one silently. Keep generated binaries out of Git; message catalogs stay in Git. Do not advertise plain `go install` as a complete installation path.

Exercise the binary produced by the current build, not a globally installed `loaf`. Cross-compilation proves buildability, not runtime support.

## Go conventions

- Use the shared four-space formatter, ordinary Go naming, and small packages organized around responsibilities. Do not run standalone gofmt on committed files: it restores tab indentation. Match adjacent code before adding a new convention or dependency.
- Return errors from library and use-case code; keep exit decisions at command entry points. Wrap underlying errors with context and `%w` where callers need `errors.Is` or `errors.As`. Return stable codes and structured parameters for CLI rendering rather than localized business-layer strings.
- Make client operations cancellable with context and close files, pipes, and SSH processes on exit. Cancelling a log stream or client request must not implicitly stop an accepted remote run.
- Keep subprocess argument construction in the SSH/sync boundary. Do not spread remote command strings across command handlers.
- Use standard Go tests, table-driven cases where useful, and fixtures near the code in `testdata`. Prefer `t.TempDir`, `t.Setenv`, and `t.Cleanup` to shared machine state. Add a dependency only when it solves a concrete need.
- Document exported symbols and non-obvious constraints. Comments explain behavior and reasons, not development history. Avoid unrelated refactors and generated-file churn.

## CLI and data contracts

- Discover the project from the nearest `loaf.toml`, then the Git worktree root, then the current directory. Target precedence is `--target`, project-local `loaf use`, then TOML `target`; never inherit another project's last machine.
- `logs`, `stop`, and `pull` without an ID select the current project's latest submitted or submission-uncertain run. Keep its bound target and location even after the default target changes. Proven preflight/sync failures do not replace it. Ambiguity is an error, not a reason to select an older run.
- Preserve argv after `run --` without Shell interpretation. TOML `run.command` and optional `run.setup` are explicit `/bin/sh -c` commands. Setup failure prevents training; setup's `cd`, exports, or environment activation do not persist into the separate command process.
- Freeze output selection when submitting. Default downloads go to `loaf-results/<run-id>/`, preserve output subpaths, and do not delete extra local files. Report missing outputs instead of claiming an empty download succeeded.
- Ordinary `check` is read-only remotely. Its explicit `--runner` option verifies the bundled runner by uploading only private, versioned Loaf files; it does not install experiment dependencies or start training.
- Core commands support `--json` from their first implementation. Keep stdout machine-readable and noninteractive; do not mix in prompts or progress. Finite commands return a versioned result, while following logs use NDJSON.
- Preserve JSON keys, status values, error codes, and time formats across languages. Operation exits are 0 for success, 2 for parameters/configuration, 3 for unconfirmed submission/stop, and 1 for other failures. The training exit code belongs in the run result, not the SSH client's exit status.
- Keep software, CLI schema, and record format versions distinct. Unknown record formats must fail without overwriting data. Preserve existing records, output locations, and active supervisors during upgrades.

## Remote execution and file safety

- Reuse SSH Config, agents, jump hosts, and host-key verification. Never store passwords, disable verification, or rewrite the user's SSH configuration. Parse pasted SSH connection arguments; reject appended commands, pipelines, and redirection.
- Send user argv, environment, and run parameters as structured input to the runner. OpenSSH remote commands still pass through a Shell: centrally quote the fixed runner invocation and rsync paths even when local subprocesses use argument arrays.
- Verify project ownership before writing or deleting. Reject root, the home directory itself, mismatched project markers, and nonempty unowned directories. Restrict source deletion to the managed cache; reject escaping output paths and symlinks.
- Sync actual working files, including uncommitted changes. Honor Git tracking and ignore semantics, then apply Loaf's exclusions even to tracked files. Cover nested rules, negation, Unicode, spaces, deletions, and safe links; do not pass `.gitignore` directly to rsync as if the syntaxes were identical.
- Hold the remote project lock across receiving source and copying the run's work directory. Give each run an independent copy, not writable hard links. Failed sync must not start training; later sync must reconcile stale files.
- Save the run ID and location locally before submission. Claim the ID exclusively on the remote side. Lost confirmation means query that same ID; never automatically resubmit under a new ID.
- The supervisor owns waiting, process-group termination, and final records. Verify boot ID, process start identity, and ownership; `kill -0` alone is insufficient. Keep signaling and reaping coordinated to avoid reused PID/PGID mistakes. Preserve actual exit codes/signals; report unknown or unconfirmed outcomes honestly.
- Detach accepted runs from SSH file descriptors. Stop requests must prevent setup from advancing to training. Write records atomically with locking and appropriate durability; stop must not overwrite a natural terminal result.
- Use the SSH user's existing privileges. Do not install training dependencies, source guessed Shell startup files, change package mirrors, auto-upload `.env`, or print secret values. Mirror/proxy settings are explicit project environment settings. Loaf is not a sandbox for untrusted training code.

## Internationalization

Use `go-i18n` templates and `x/text/language` matching in the local rendering layer. Keep stable message IDs, complete sentences, and named placeholders in `internal/i18n/locales/active.en.toml` and `active.zh-Hans.toml`.

- Update both catalogs with every user-visible change, including root/subcommand help, flag descriptions, parser errors, prompts, statuses, progress, and recovery hints. Missing translations or invalid templates must fail checks.
- Select language by `--lang`, `LOAF_LANG`, then the global `language` setting. For `auto`, use the first nonempty `LC_ALL`, `LC_MESSAGES`, or `LANG`, falling back to English when unmatched. Normalize locale tags and distinguish Simplified and Traditional scripts. Reject unsupported explicit choices rather than ignoring them.
- Keep language preference out of shared project TOML. Do not alter remote `LANG`/`LC_*`, rewrite script logs, or translate external-tool diagnostics. JSON `message` and `hint` may be localized; Agents use codes and structured fields.
- Test required plural forms, placeholder compatibility, and actual English/Chinese help layout. Do not add language-specific business branches or a translation service.

## Match verification to the change

| Changed area | Evidence to obtain |
| --- | --- |
| Flags, defaults, config, JSON | Parser/config tests, both help/error renderings, stdout/stderr and exit behavior |
| Source filtering or paths | Real temporary trees for nested ignores, tracked exclusions, spaces, Unicode, deletion, and escaping links; SSH/rsync integration for transfer behavior |
| Local index or remote records | Real filesystem tests for concurrent updates, atomic writes, unknown formats, and recovery |
| Start, query, logs, stop | Real Linux SSH tests for disconnects, duplicate IDs, lost confirmation, child groups, natural-exit/stop races, invalid identities, and retained exit results |
| Output download | Missing paths, incremental retry, destination isolation, and escape rejection |
| i18n | Both catalogs render; protocol fields and remote execution parameters remain unchanged |
| Runner embedding or packaging | Unpack the actual archive and run its CLI against the fixture |
| README or agent instructions | Read the affected document as a whole; check examples, links, and consistency without unrelated Go tests |

Automated integration uses temporary keys, directories, accounts, and CPU scripts, not personal SSH files, research data, or rented GPUs. Assert an unrelated sentinel process survives stop tests. Synchronize on observable readiness rather than relying on fixed sleeps. Clean only resources created by the test.

A real GPU workflow is separate acceptance work on an explicitly authorized existing target. Do not infer authorization to connect, spend money, stop a machine, or publish from permission to edit code.

## Documentation, Git, and handoff

- Keep `README.md` and `README.zh-Hans.md` aligned with actual availability, commands, configuration, and supported platforms. Write for users; do not add implementation diaries or invented install instructions.
- `docs/` is local-only and Git-ignored. Do not force-add it, link to it from public entry points, or make builds/tests depend on it. This repository rule is not a global `docs/` exclusion for projects synchronized by Loaf.
- Leave CONTRIBUTING, governance/Issue/PR templates, SECURITY, CHANGELOG, ADR, CODEOWNERS, and automated dependency-update configuration deferred until requested. Do not recreate them as part of ordinary feature work.
- Before handing off, inspect the complete affected diff, check public-contract consistency, and remove debug output or accidental artifacts. Update this guide when real commands or package boundaries change; keep it independently usable without local planning notes.
- When commits are authorized, use English subjects and bodies in `type(module): description` format, for example `feat(cli): add machine connection management`. Group coherent, tested behavior changes; describe the change rather than milestone codes.
- Summarize the result, validation actually performed, and material unresolved limitations. Separate build success from runtime evidence. Do not commit, push, tag, publish, or modify a real server without the corresponding user authorization.
