# Agent guide for Loaf

Loaf is a Go CLI for running local research projects on SSH machines, with optional explicitly authorized AutoDL acquisition. The core workflow is connect, sync, run in the background, inspect logs, stop, and retrieve outputs. Environment checks, English and Simplified Chinese, and structured JSON output are native features.

Keep GPU workload scheduling, queues, experiment retries, central services, Web UI, and MCP out of scope. Cloud acquisition is explicit and foreground-only; it never starts training implicitly, releases instances or automatically shuts them down. Foreground machine operations and explicit completed-task cleanup complement, rather than replace, the durable experiment workflow.

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
internal/cli/     Kong schema, dispatch, Huh/native interaction, human and JSON output
internal/client/  Projects, Machines and Resources application workflows
internal/i18n/    Locale selection and embedded message catalogs
internal/config/  TOML, project discovery, preference merging and value validation
internal/task/    Frozen run locators, labels, cleanup and observation values
internal/state/   Concrete local/remote SQLite stores and cloud receipt SQL
internal/cloud/   Resource conditions, optional inventory capability, acquisition and private intent receipts
internal/autodl/  Main/sub login, ordinary/Pro web APIs and separate Pro developer API
internal/credentials/  Explicit OS keyring or private unencrypted credential files
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

Keep command handlers thin. Compose dependencies at the entry point with constructors; use consumer-owned interfaces for remote control, source sync, and the run index. Cloud lifecycle and optional read-only inventory have a small provider boundary with two concrete AutoDL products. Plain values and pure helpers do not need interfaces. Do not introduce a DI container, generic Repository, plugin/provider framework, or backend registry.

`run` and `remote` must not depend on CLI rendering or i18n. `remote` depends on shared wire types and system capabilities, not local configuration, Kong/Huh, the SSH client, or `bundle`. Keep Go support packages internal; this release does not expose a Go SDK.

## Application and state boundaries

The CLI declares arguments once through Kong, invokes typed client requests, and renders results. Projects, Machines and Resources own complete workflows; no parser, Huh object, localizer or output-mode boolean enters their APIs. Login uses one native input session; ordinary prompts may use Huh. Keep finite JSON, NDJSON, native streams and stderr progress distinct.

Use the pure `run.ResolveExecution` function for preview and submission command/setup/environment/output normalization. Keep the existing `Submit` entry point usable; do not make callers construct a new plan or perform side effects first. Public previews expose environment names, never the private values in `ExecutionSpec`.

The standalone depguard tool enforces a small set of forbidden dependency directions: use cases do not depend on CLI/i18n; remote code does not depend on local configuration or transport; CLI does not access SQL or AutoDL directly; lower-level packages do not call back into application services. Keep these deny-only rules focused rather than maintaining a package-wide allowlist.

Use `database/sql` with `modernc.org/sqlite` and explicit short transactions. Local state is `<StateDir>/state-v1/state.db`; remote control state is `<project>/.loaf-control-v1/state.sqlite3`. Keep credentials and run payload files outside the databases. WAL, FULL synchronous, bounded lock waits and private permissions are required. SQL transactions never span network calls, copying or process runtime; existing OS lifetime/control/payload locks remain responsible for those operations.

v0.5 starts new schema-1 stores without importing old JSON state. There is no migration, dual-write or fallback backend. Keep software, database schema and wire versions independent. A user must use the matching old CLI for old tasks and reconcile uncertain old cloud receipts before starting new acquisitions. Do not build an old-file preservation test matrix; test the current schema and actual recovery risks.

## Build and development commands

Run from the repository root. Commands requiring Go sources, a module, or a script apply only when those inputs exist. If `go.mod` is absent, there is no Go build/test entry point yet; do not bootstrap one for a prose-only task.

| Task | Entry point | Requirement |
| --- | --- | --- |
| Format Go changes | `go run ./scripts/format.go` | Standard Go AST formatting with four-space indentation; use `-check` in CI |
| Test one package or case | `go test ./internal/run -run TestName` | Example: replace the package and test selector with the affected ones |
| Run ordinary tests | `go test ./...` | Go module and source present |
| Static checks | `go vet ./...` | Go module and source present |
| Dependency boundaries | `go tool depguard ./...` | Standalone tool pinned in go.mod; shared checks run it after formatting |
| Called-dependency vulnerabilities | `go tool govulncheck ./...` | Tool version is pinned in go.mod; requires access to the public vulnerability database |
| Check concurrency changes | `go test -race ./...` | A supported local or CI runtime; Linux process cases still require Linux |
| Build the complete CLI | `bash scripts/build.sh` | Add this working script with the first implementation |
| Run shared checks | `bash scripts/check.sh` | Use once implemented; local and CI checks share this entry point |
| Run SSH integration tests | `bash scripts/integration.sh` | Use once implemented with the isolated fixture and a Docker-compatible engine |
| Check prose changes | `git diff --check`, then inspect links and examples | Inspect new untracked files as well; Go tests are not needed |

Start with affected-package tests. For Go changes, follow with ordinary tests and vet; use race, integration, and packaging checks when the changed behavior calls for them. Report an unavailable check and its effect instead of substituting a passing unrelated command.

The target software release is recorded in `VERSION`; default build identities append a development revision and timestamp, while explicit `VERSION` overrides select an exact identity. Keep CLI schema and remote record formats independent of software release numbers.

The complete build has one owner: `scripts/build.sh` builds Linux amd64/arm64 runners, writes generated assets under `internal/bundle`, then builds the client with embedded assets. A mise build task, CI, and release jobs call this same script. Do not add parallel Make, Task, or release-tool build paths.

`scripts/package/` only collects metadata and license texts from those built binaries, and generates download checksums; it does not compile or publish. Build archives in fresh temporary staging directories, not reused package trees. Each archive includes `BUILD.json`, Go/dependency notices, both READMEs, the Skill and examples. Keep unavailable VCS metadata unknown. The tag-triggered release workflow reuses CI before publishing; preparing that workflow does not authorize pushing tags or creating a release.

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

- `run` includes prerequisite checks, source sync and independent-copy preparation; do not require manual `check`/`sync` before submission. `run --wait` and `ps <id> --wait` are bounded observation, not remote runtime limits. Defaults are immediate return after acceptance, configurable project/user waiting, and 100 log tail lines; preserve explicit zero overrides. List queries are bounded and preserve partial results.
- `exec` is an OpenSSH foreground machine command, not project submission: no sync/setup/run ID, remote home by default, explicit `--cwd` and project/user `[exec].cwd` preferences. Preserve argv; Shell interpretation requires an explicit Shell command. Human streams remain native; JSON uses bounded byte-preserving NDJSON. Confirmed nonzero remote exits and unconfirmed SSH-255/cancellation results are distinct; never replay an unconfirmed command. `ssh` is a native terminal. `forward` is a dedicated foreground loopback tunnel with inherited backgrounding/multiplexing disabled; cancel only its connection, not the user's master or training. Neither accepts JSON; tunnel setup is not service-health proof.
- `run --dry-run` validates the full local submission plan and reports per-field provenance, source bytes and environment names only; never access SSH/credentials, allocate IDs or write state. `sync --dry-run` retains its narrower existing contract.
- Once the initial locator is saved, preparation failures retain the run ID and frozen binding in the result. Saved `preparing` records are accessible by explicit project-scoped ID, including offline inspection; history lists and latest selection still exclude them. An explicit live query observes that binding without starting or resuming it, and does not infer submitted acceptance from preparation.
- `wait` freezes explicit IDs or exact local tags once (1–1000 tasks), observes saved bindings with complete partial coverage, and never stops or schedules workloads. Offline `inspect`/`ps --local` may expose dated public observations and a separate last failure; cached status never replaces live `status`.
- Preserve verified remote facts when local state/checkpoint writes or output consumption fail. `state_error` carries a local persistence failure's stable code; history and wait report `state_errors` separately from `query_error`/query-error coverage. These local failures must not become `last_query_failure` or erase verified status, phase or exit results. A failed log consumer does not acknowledge its chunk.
- `logs --resume-file` auto-checkpoints only successful human terminal writes. JSON/pipe consumers explicitly acknowledge processed cursors with offline `--ack-cursor`; writes cannot rewind the bound stream. Positive `--recovery-budget` bounds cumulative disconnected waiting; zero retains one retry, and default following remains single-connection.
- `outputs` is bounded read-only metadata within frozen outputs, with explicit depth/entry/root coverage; never upload helpers by default. `--prepare-runner` is explicit preparation. Batch `pull <ids> --into` isolates run-ID destinations and retains per-ID partial results; concurrency is opt-in and interactive authentication stays serial. Preserve single-run exact `--to`.
- `check --targets` observes at most 32 distinct aliases with bounded per-target timeouts and partial results. `--report` exports only ordinal IDs and allowlisted steps/platform labels/timings/codes/trace; no endpoints, paths, commands, environments or raw diagnostics. Ordinary check remains read-only remotely; preparation requires `--runner`.
- `ps` list filters use saved attempt time (RFC3339), original alias, and observed execution status. Apply local filters before bounded observation; expose examined/matched/unresolved/unexamined coverage rather than silently scanning to fill matches. Preserve unknown query rows and deterministic order. Human interactive observations stay serial to avoid overlapping authentication prompts; batch observations use bounded concurrency. Single IDs reject list filters and target redirection. `[ps].limit` follows flag/project/user/default precedence, and explicit zero remains invalid.
- `ps --local` and `inspect <id>` read only saved project-scoped facts, across software versions, without SSH, credential access or helper deployment. Expose required/current versions and safe frozen instance identity, not raw SSH args or credential refs. Local status remains unknown except retained cleanup summaries; reject live status filters/nonzero wait. Names/tags are immutable navigation, never identities; exact AND tag/name filters precede bounded observation. Keep labels separate from frozen locators in the local SQLite schema. Equal attempt times sort stably by ID, while implicit latest still rejects ambiguity.
- `targets [name]` reads local settings; `remove <name>` removes only an alias. Use existing `add --replace` for connection updates. Do not delete locators, redirect older runs, rewrite project TOML, or silently reselect a default when removing a machine.
- Normal `run --wait` shows preparation and live log feedback. JSON finite commands keep one result; `logs -f --json` and foreground `exec --json` use NDJSON chunks followed by a final result/error. Bound log text to 256 KiB per chunk, report truncation, and preserve source byte offsets. Do not expose private execution environments or raw run records in public observations.
- Discover the project from the nearest `loaf.toml`, then the Git worktree root, then the current directory. Target precedence is `--target`, project-local `loaf use`, then TOML `target`; never inherit another project's last machine.
- Sync policy scalars use explicit flags, project `[sync]`, global `[sync]`, then built-ins (`mode = "mirror"`, `respect_gitignore = true`). Preserve explicit false values and merge each scalar independently. `exclude` appends across scopes and cannot negate built-in exclusions. `update` mode retains removed/newly excluded cache files; show that consequence and the effective policy.
- `sync --dry-run` only selects local files and reports policy/exclusions, without requiring a target, connecting, deploying, or writing state. A real sync deploys the bundled receiver. Keep the preview distinct from a remote transfer simulation or a frozen snapshot.
- `logs`, `stop`, and `pull` without an ID select the current project's latest submitted or submission-uncertain run. Keep its bound target and location even after the default target changes. Proven preflight/sync failures do not replace it. Ambiguity is an error, not a reason to select an older run.
- `stop` sends one request, then observes the same bound run. The runner defaults to 10s grace; explicit `--grace`, project `[stop]`, and user `[stop]` override in that order (0s to 5m). Preserve absent and explicit zero settings. The first accepted request fixes the policy. Wait for grace plus 5s, capped at 5m, with SSH connection allowance; an unconfirmed or canceled result is `STOP_UNCONFIRMED` (exit 3), not `stopped`.
- Preserve argv after `run --` without Shell interpretation. TOML `run.command` and optional `run.setup` are explicit `/bin/sh -c` commands. Setup failure prevents training; setup's `cd`, exports, or environment activation do not persist into the separate command process.
- Freeze output selection when submitting. Default downloads go to `loaf-results/<run-id>/`, preserve output subpaths, and do not delete extra local files. Report missing outputs instead of claiming an empty download succeeded.
- `pull` uses frozen paths unless repeatable literal `--path` overrides them for one invocation. Project/user `[pull]` settings merge `base_dir` and optional `checksum` independently; built-ins are `loaf-results` and false. Relative bases use the project root and append the run ID; explicit `--to` is exact and relative to the caller's directory. Exclude the current configured result base from source selection; arbitrary one-off destinations require ordinary ignore/exclude configuration.
- Downloads use native rsync quick-check by default and explicit `--checksum`/false overrides, never mirror deletion or in-place writes. Validate all selections before transfer; download available selections but return `OUTPUT_MISSING` (exit 1) with missing paths. Failed transfers retain partial observations, not a claimed completed-file count. Running pulls are live copies, not checkpoint snapshots. Locally lock only the same destination; remotely hold the separate shared payload lock, not source/execution locks.
- `clean` requires explicit project-scoped IDs and one human confirmation or noninteractive `--yes`; never default to latest/all or stop active tasks implicitly. Preview is read-only and does not deploy helpers; actual cleanup revalidates ownership, fresh terminal outcome and locks. Delete only selected run payload directories, retaining the separate deletion receipt and ID claim so delayed starts cannot execute again. Keep only the necessary public terminal summary locally, not private request environments. Cleanup availability metadata does not replace the execution status; latest selection remains bound and data operations report `RUN_CLEANED`. Partial/unconfirmed deletion must be recoverable for the same explicit IDs without deleting local downloads or adjacent data.
- Cleanup eligibility requires confirmed submission and a fresh terminal result. `clean` does not reclaim unsubmitted preparation data. Supporting that later requires a durable no-start protocol that excludes delayed launch and copy operations; do not broaden cleanup eligibility merely because a local locator says `preparing`.
- Require the current software version in run locators, control requests and remote records. Automatically deploy and verify the bundled current helper before runner operations, repairing a missing or mismatched executable; do not fall back to saved older executables or hot-replace supervisors. Cross-version tasks return `VERSION_MISMATCH`. Preview remains read-only. Unknown record/index formats fail without mutation; no compatibility adapters or silent migration. Output inspection never creates remote project/run directories or requires a successful/live training process.
- Ordinary `check` is read-only remotely. Its explicit `--runner` option verifies the bundled runner by uploading only private, versioned Loaf files; it does not install experiment dependencies or start training.
- Log cursors bind software version, project/run/location, file identity, source-byte offset and a bounded integrity guard. Default following stays single-connection. Explicit `logs -f --reconnect` or `--resume` may reconnect once from the last successfully consumed cursor, never replay a tail, restart after an output-consumer failure/cancellation/end, or ignore changed logs. Resume rejects explicit tail and non-follow mode; `LOG_CHANGED` requires an explicit fresh tail. Cursor fields are transient, not a remote record migration.
- `--trace` is opt-in for finite command SSH diagnostics: bounded safe stage/timing/code/retry-reason events and complete resolution/private-master/reconnect/read-retry counters. Never collect commands, endpoints, request bodies or environments; JSON adds one trace object and human traces use stderr. Keep stream/terminal/foreground exec behavior unchanged.
- Core commands support `--json` from their first implementation. Keep stdout machine-readable and noninteractive; do not mix in prompts or progress. Finite commands return a versioned result, while following logs use NDJSON.
- Preserve JSON keys, status values, error codes, and time formats across languages. Operation exits are 0 for success, 2 for parameters/configuration, 3 for unconfirmed submission/stop/exec/cleanup/cloud mutation, and 1 for other failures. The training exit code belongs in the run result, not the SSH client's exit status.
- Keep software, CLI schema, and record format versions distinct. Unknown record formats must fail without overwriting data. Preserve existing records, output locations, and active supervisors during upgrades.

## Remote execution and file safety

- Reuse SSH Config, agents, jump hosts, and host-key verification. Never store passwords, disable verification, or rewrite the user's SSH configuration. Parse pasted SSH connection arguments; reject appended commands, pipelines, and redirection.
- Cloud runner operations use one command-owned private OpenSSH master per frozen binding and argument set; native rsync shares it without a custom relay. Refresh connection details when establishing a new master, not before each channel. Never reuse or close a user-owned master. Keep master readiness, lifetime, cancellation, orphan cleanup and fail-closed mux clients in the SSH boundary. Static targets and foreground `exec`/`ssh`/`forward` keep their existing contracts. Finite read operations/downloads may retry one recognized transient failure with cancellable backoff; never replay source sync, uploads, launch, stop, clean, foreground commands or default log following. Explicit cursor recovery follows the log contract above.
- Preserve original fault codes, hints and safe diagnostics through runner preparation; `error.stage` identifies the most specific failure. Public run observations retain `submission` independently of execution `status`, including confirmed acceptance followed by an unreachable observation.
- Cloud targets freeze product, account/credential reference and instance identity in saved runs, not an endpoint or password. Refresh only that instance's connection; changing/removing aliases must not redirect history. Cloud password authentication uses native SSH askpass and a private temporary local socket. Keep passwords out of files, argv, environment and ordinary JSON; reject host-key confirmation/passphrase prompts. Strip configured platform token variables from SSH/rsync child environments, including when SendEnv is broad. Only explicit `cloud credentials --show-password` may display a password. Keep native static SSH behavior intact.
- Send user argv, environment, and run parameters as structured input to the runner. OpenSSH remote commands still pass through a Shell: centrally quote the fixed runner invocation and rsync paths even when local subprocesses use argument arrays.
- Verify project ownership before writing or deleting. Reject root, the home directory itself, mismatched project markers, and nonempty unowned directories. Restrict source deletion to the managed cache; reject escaping output paths and symlinks.
- Sync actual working files, including uncommitted changes. Honor Git tracking and ignore semantics by default, then apply Loaf's exclusions even to tracked files. Disabling `respect_gitignore` does not disable Loaf exclusions. Cover nested rules, negation, Unicode, spaces, deletions, and portable relative links; do not pass `.gitignore` directly to rsync as if the syntaxes were identical. Skip absolute, escaping, broken, and excluded-target links rather than rewriting them.
- Use native rsync `-e` and `--rsync-path`: system rsync owns its protocol and mirror deletion. Keep file selections and native include filters transient. The receiver checks project context and receiving destination, holds the lock, and writes the completion marker; do not insert metadata into the stream, add a private SSH relay, or whitelist version-specific server flags. Distinguish rsync's argument tokenizer from remote-shell quoting; cover both macOS openrsync and Linux GNU rsync.
- Hold the remote project lock across receiving source and copying the run's work directory. Give each run an independent copy, not writable hard links. Failed sync must not start training; later sync must reconcile stale files.
- Save the run ID and frozen location locally as `preparing` before source sync. During sync the remote side exclusively claims that ID and prepares the independent work copy. Persist `uncertain` before the single launch request, then record confirmed acceptance as `submitted`. Lost confirmation means query that same ID; never automatically resubmit under a new ID.
- The supervisor owns waiting, process-group termination, and final records. Verify boot ID, process start identity, user ownership, and the lifetime lock; `kill -0` alone is insufficient. Keep signaling and reaping coordinated to avoid reused PID/PGID mistakes. Preserve actual exit codes/signals; report unknown or unconfirmed outcomes honestly. The short per-run control lock serializes stop acceptance, phase admission and final publication; it is separate from the supervisor's lifetime lock. Cleanup covers the managed group, not processes that deliberately leave it.
- Detach accepted runs from SSH file descriptors. Stop requests must prevent setup from advancing to training. Write records atomically with locking and appropriate durability; stop must not overwrite a natural terminal result.
- Use the SSH user's existing privileges. Do not install training dependencies, source guessed Shell startup files, change package mirrors, auto-upload `.env`, or print secret values. Mirror/proxy settings are explicit project environment settings. Loaf is not a sandbox for untrusted training code.

## Cloud acquisition contracts

- `cloud search` and `watch` are read-only. `acquire` needs explicit approval for at most one instance; `run` never rents. Project/user resource profiles are optional: flags override project settings, then user settings, independently per field. Lists replace, not union; preserve valid explicit zero/empty values. Account credential references and the independent non-overridable safety ceiling are user-owned. Freeze effective requirements before monitoring.
- `acquire --clone-from <id-or-cloud-alias>` uses the same acquisition loop with the optional `Cloner` capability, currently ordinary session-backed AutoDL only. Freeze source identity/region/image constraints in the plan and receipt. Revalidate the stopped source before cloning; always copy system and data disks, retain the source, never fall back to another region or ordinary creation. Inherit source disk expansion and a 30m wait as lowest-priority defaults; reject smaller disks and conflicting regions, preserve explicit zero wait. Inherit source image rather than image defaults; reject explicit image/spec/CUDA/system-disk flags. Clone binding via the saved request also requires verified successful data copying before `ready`; timeout/failure retains the same resource and receipt for recovery.
- Keep account identity, credential mode/location and compute product separate. `Account.DefaultProduct` is a convenience default; profile `product` and explicit `--product` override it. Bindings and receipts freeze account settings and the actual selected product; queries, power operations and reconciliation never reinterpret them using current defaults. `CredentialRef` contains only session/developer mode, one environment or keyring/file reference, and any verified main/sub identity. Keep secrets out of TOML, public JSON and run locators. Native login stores sessions and optionally the replayable SHA-1 password equivalent; never describe that digest as safe on disclosure. Validate unknown credential formats without overwrite. Reauthentication preserves verified identity; replacing a principal or changing stores never repoints historical bindings.
- Credential storage is explicitly keyring or file. New logins use flag, global `cloud.credential_store`, then keyring; existing refs keep their source absent an explicit flag. Compose the two concrete stores manually. Never fall back between them or invent encryption. File credentials live only in the private user state directory (0700/0600 on Unix), outside sync/config/index; refuse symlinks, unsafe permissions and unknown records. Logout erases only the selected entry. Keyring native unlock dialogs are OS-owned and not context-cancellable. Do not claim native Windows CLI support from a cross-platform keyring library.
- Keep web-session and developer-API auth/hosts separate. The implemented combinations are session/standard, session/pro and developer/pro; unsupported combinations fail before secret access and never fall back. Session requests for verified sub identities use the explicit subaccount namespace, retaining only known public-catalog exceptions. Use HTTPS JSON through net/http with bounded responses, timeouts and cancellation; check both HTTP and business success. Forbid redirects and never expose raw response bodies, tokens, passwords or Jupyter tokens. Do not configure an arbitrary token destination. Pro developer snapshot/status use GET query parameters; mutations are never retried by the HTTP adapter.
- Match all hard requirements, treating required unknowns as ineligible. GPU memory is per card; compute hourly prices cover the whole requested instance and use exact decimal CNY, excluding storage. Web products use price-preview API contracts, never guessed raw-price units. Requote immediately before mutation; hourly compute and daily storage remain separate. Pro web catalogs expose aggregate model/region stock and conservative CPU/RAM floors, not reserved spec capacity; unknown GPU memory/vendor must not satisfy constraints. Numeric web spec IDs and developer UUIDs are distinct. A fresh pre-create observation is not a reservation or server-side price lock. Preserve source/time and bounded pagination coverage.
- Pro's developer API lacks a verified read-only catalog/quote capability in this adapter. Do not substitute Elastic stock. Explicit unquoted allocation attempts require --allow-unquoted, cannot bypass a price cap and cannot relax unknown hardware floors. Reject unsupported capabilities before charging.
- Persist a private intent receipt before a paid/lifecycle request. Lost confirmation, cancellation during mutation, malformed responses and ambiguous server failures are unconfirmed, never automatic replay. Only mapped, confirmed no-stock rejections may permit another sequential allocation within the same frozen approval window. Creation success with failed connection retains its resource/request ID; never rent a replacement. Reconciliation reads the saved resource; --no-instance requires explicit human verification/approval.
- Foreground polling defaults to 10s with bounded wait/backoff/jitter/Retry-After. Read-only rechecks/pagination may make multiple requests within one poll. Do not spam unchanged full inventories. Finite commands/acquire return one JSON envelope; watch emits NDJSON changes/heartbeats/end or error. Power acceptance is not completion; cloud stop affects the whole instance, not one experiment. An inventory wait must not prevent stopping a different existing instance.

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
| Cloud APIs/acquisition/auth | Both product request contracts; exact price/unknown fields and pagination; intent-before-call, no ambiguous replay, cancellation, confirmed no-stock retry, concurrent acquisition, reconciliation, redacted output; real isolated SSH/rsync with ephemeral password auth and rejected unknown host keys |
| README or agent instructions | Read the affected document as a whole; check examples, links, and consistency without unrelated Go tests |

Automated integration uses temporary keys, directories, accounts, and CPU scripts, not personal SSH files, research data, or rented GPUs. Assert an unrelated sentinel process survives stop tests. Synchronize on observable readiness rather than relying on fixed sleeps. Native interrupted-transfer readiness uses a created temporary file and completed small selection, not positive temporary-file size: openrsync can buffer while it is zero. The shared integration path runs host-native and Linux-native private-master loss recovery for reads, rsync and explicitly resumable logs, using only fixture-owned processes. Clean only resources created by the test.

Cloud recovery tests use fresh CLI processes and simulated HTTP to verify durable receipts without replaying ambiguous mutations. `TestLinuxCloudCredentialStores` runs the native Secret Service backend and login lifecycle in an unprivileged container with a private D-Bus session; it also checks unavailable-bus failure. Keep host vaults and real credentials out of this fixture. Run it through `scripts/integration.sh` so the Linux test binaries and packaged CLI come from the shared build path. Simulated platform HTTP is not real cloud or billing acceptance.

The optional `examples/torch` uses an existing PyTorch environment and generated data; it is not a Go-test or CPU-fixture dependency. Its downloaded parameter verifier uses only Python's standard library. Choose an explicit interpreter when remote noninteractive PATH differs; never source guessed startup files or install dependencies automatically.

A real GPU workflow is separate acceptance work on an explicitly authorized existing target. Do not infer authorization to connect, spend money, stop a machine, or publish from permission to edit code.

## Documentation, Git, and handoff

- Keep `README.md` and `README.zh-Hans.md` aligned with actual availability, commands, configuration, and supported platforms. Write for users; do not add implementation diaries or invented install instructions.
- `docs/` is local-only and Git-ignored. Do not force-add it, link to it from public entry points, or make builds/tests depend on it. This repository rule is not a global `docs/` exclusion for projects synchronized by Loaf.
- Leave CONTRIBUTING, governance/Issue/PR templates, SECURITY, CHANGELOG, ADR, CODEOWNERS, and automated dependency-update configuration deferred until requested. Do not recreate them as part of ordinary feature work.
- Before handing off, inspect the complete affected diff, check public-contract consistency, and remove debug output or accidental artifacts. Update this guide when real commands or package boundaries change; keep it independently usable without local planning notes.
- When commits are authorized, use English subjects and bodies in `type(module): description` format, for example `feat(cli): add machine connection management`. Group coherent, tested behavior changes; describe the change rather than milestone codes.
- Summarize the result, validation actually performed, and material unresolved limitations. Separate build success from runtime evidence. Do not commit, push, tag, publish, or modify a real server without the corresponding user authorization.
