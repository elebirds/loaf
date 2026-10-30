# Agent guide for Loaf

Loaf lets people and Agents run local research projects on SSH machines: connect,
sync, submit, inspect logs, stop, and retrieve outputs. Keep that path easy to
understand and use. The slogan is **Code here. Run anywhere.**

## Product direction

- The CLI is the shared workflow for people and Agents, with readable output,
  English/Simplified Chinese, and structured JSON. Ordinary commands call typed
  application workflows directly; do not recreate dashboards or command browsers.
- Only `cloud acquire -i` needs a multi-step TUI. Login, machine registration and
  destructive actions may use command-local input. A TTY is capability, not consent;
  only explicit `-i` permits forms, and it never implies `--yes`.
- Direct execution needs no Coordinator, Bridge or Agent adapter. Optional event
  delivery and a single-target FIFO queue are supported. Keep experiment retries,
  preemption, DAGs, multi-node scheduling, required central services and Web UI out of scope.
- Solve the current user problem before adding infrastructure. Queries and display
  changes usually need clear results, not new durable state or recovery protocols.
  Invest more where a mistake can duplicate work, spend money or delete data.

## Architecture and invariants

- `cmd/` composes dependencies; `internal/cli` parses and renders; `internal/client`
  owns Projects, Machines and Resources workflows. Domain packages own execution,
  cloud selection and transfer; SSH, AutoDL and state packages implement boundaries.
  Use small consumer-owned interfaces for actual side effects, not plain values.
- Keep command parsing, forms, locale and output-mode flags out of application
  requests. `run`/`remote` do not depend on CLI or i18n; remote code does not depend
  on local config or transport. The delivery SDK is not a public Loaf control SDK.
  Do not add a DI container, generic repository, provider registry or form engine.
- Preserve public argv, JSON keys/codes, explicit zero/false/empty overrides, frozen
  task identities and output locations. Consult the relevant manual and nearby tests
  when changing a contract. A query failure is not an execution failure.
- Keep uncertain mutations bound to their saved identity; never automatically
  repeat a possibly accepted launch, payment, stop or deletion. Deletion requires
  explicit IDs; stop retains its existing frozen binding and authorization rules.
- Cloud acquisition is explicit and foreground-only. It never starts training,
  releases instances or shuts machines down automatically. Editing code does not
  authorize using real servers, credentials, paid resources or Agent sessions.
- Keep secrets and private execution values out of sync, public events and output.
  Preserve user data and unrelated work. Unknown formats fail without overwriting;
  no historical migration or active-Supervisor upgrade is part of current development.
  Reassess the version/state compatibility promise before 1.0.

## Work on the relevant path

1. Read `git status --short` and the current diff. Do not reset or clean the checkout.
2. Read the target, callers and nearby tests. Use the existing manuals rather than
   treating every historical edge case as a new task:
   - CLI/config/output: [guide](manual/en/guide.md), [JSON](manual/en/json.md).
   - Execution/state/transport: [remote contracts](internal/remote/AGENTS.md) and
     [development](manual/en/development.md); these also apply to their client callers.
   - Cloud/account/provider changes: [cloud contracts](internal/cloud/AGENTS.md) and
     [cloud guide](manual/en/cloud.md), including adapters outside that directory.
   - Queue/delivery: [async guide](manual/en/async.md),
     [SDK compatibility](sdk/agentdelivery/COMPATIBILITY.md).
   - Packaging/platforms: [installation](manual/en/installation.md),
     [upgrading](manual/en/upgrading.md). Cross-builds are not runtime evidence.
3. Check `command -v` for tools and use reviewed `mise.toml`/PATH versions. Root and
   `sdk/agentdelivery` share `go.work`; do not tidy either as an unrelated standalone
   module. Run `go work sync` after dependency changes and inspect the declaration
   diff. Go may also refresh checksum files for the current host/cache.
4. Implement the smallest complete change with its examples and relevant tests.
   Reuse existing entry points and checks; add no new gate or test platform by default.

## Development and delivery

- Go uses the shared four-space formatter, not standalone gofmt:
  `go run ./scripts/format.go -- path.go directory/` (`-check` is read-only).
- Start with affected-package tests. For Go changes run ordinary tests and vet in
  both workspace modules. Use race or SSH integration when the changed behavior
  involves concurrency or transport, not as a requirement for prose edits.
- `bash scripts/check.sh` is the shared full check; `bash scripts/integration.sh`
  runs the isolated Linux SSH fixture. Tests use temporary data, keys and CPU jobs.
- `bash scripts/build.sh` is the only complete build owner: embed Linux runners,
  build clients and package archives. Exercise its current binary, not a global
  installation. Keep generated assets out of Git; do not advertise plain `go install`.
- Keep READMEs, bilingual `manual/` and the standalone Skill aligned. User-facing
  copy changes update both locale catalogs. Grammar changes regenerate command
  references with `LOAF_UPDATE_MANUAL=1 go test ./internal/cli -run TestCommandReferenceAndExamples`.
- `docs/` is ignored local work, not a public dependency. Do not force-add it or
  introduce governance/release systems as part of ordinary feature work.
- Inspect the final diff and report the result, relevant checks and actual gaps.
  Commit/push/tag/publish only when authorized; commits use English
  `type(module): description` subjects and bodies.
