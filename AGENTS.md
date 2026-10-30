# Agent guide for Loaf

Loaf is a lightweight remote compute tool for developers, researchers and Coding
Agents. Optimize for ready-to-use defaults, little configuration, and low friction
from local development to remote compute.

## Product & Engineering Principles

Build the simplest useful solution, preserve essential invariants, and add
complexity only when a concrete requirement justifies it.

- **Product-first, not reliability-first.** Start with the real problem and who
  benefits. Prefer existing capabilities or a simpler solution; new state,
  interfaces and maintenance must earn their cost. A theoretical failure alone
  does not justify another state machine, receipt or recovery protocol.
- **Agent-first, CLI-first, human-friendly.** Provide direct, composable commands,
  readable English/Simplified Chinese output, stable and complete JSON/NDJSON,
  discoverable parameters, actionable errors and explicit next actions. Minimize
  implicit effects, blocking input, repeated calls and context consumption.
  A Skill cannot substitute for a usable programmatic CLI.
- **Selective TUI.** Use multi-step forms only where continuous human decisions
  benefit, currently GPU selection, images and budget review via `cloud acquire -i`.
  Run, Status, Logs, Pull, Queue and Notify stay direct CLI workflows. Do not add
  dashboards, management centers, cross-command menus or a general form engine.
  Forms edit a draft; business rules and facts belong to the same use cases as flags.
- **Proportionate reliability.** Queries and presentation need simple, accurate
  results; repeatable network reads may fail and be queried again. Submission,
  payment and destructive actions need stronger identity and authorization rules.
  Keep those safeguards at their owners, not in every ordinary operation. Automatic
  recovery from every possible failure is not a product goal.
- **Simplicity is an architectural requirement.** Use clear names, focused
  responsibilities, one owner per business fact, direct control flow and mature components. Avoid
  speculative frameworks, redundant state conversions, hidden recovery paths and
  abstractions or file splits without a functional reason. Judge clarity,
  change cost and user benefit, not LOC, package count or abstraction count.
- **Minimal infrastructure, progressive complexity.** SSH, sync and direct Run
  must not require deploying a daemon, Coordinator, database server or Agent service.
  Advanced capabilities are opt-in and cannot add prerequisites to the basic path.
  Reuse OpenSSH, rsync and existing remote environments; do not expose internal
  complexity as user setup.
- **Scope discipline.** Own the external compute lifecycle: acquisition,
  connection, transfer, execution, queuing, observation and notification. Do not
  take over workload logic or grow into a general cloud orchestrator, training or
  experiment-management system, or Agent Runtime. Optional FIFO queuing and delivery
  do not authorize experiment retries, preemption, DAGs, multi-node policies or
  central services.
- **Fast, maintainable iteration.** Extend existing use cases instead of parallel
  implementations. Do not require refactoring on every change or turn historical
  design choices into permanent rules. Unreleased experiments may change deliberately;
  honor promised stable interfaces, running tasks and users' data.
- **Sufficient verification, not ceremony.** Verify the changed behavior and its
  material consequences. A message edit does not need a system audit; paid requests
  and process lifecycle changes need their concrete failure paths checked. Reuse
  existing automation; more checks, baselines or evidence paperwork are not progress
  by themselves.

## Responsibilities and contracts

- `cmd/` composes dependencies; `internal/cli` parses and renders; `internal/client`
  owns Projects, Machines and Resources workflows. Domain packages own execution,
  cloud selection and transfer; SSH, AutoDL and state packages implement boundaries.
  Use consumer-owned interfaces for actual side effects, not plain values.
- Keep parser, forms, locale and output-mode flags out of application requests.
  `run`/`remote` do not depend on CLI or i18n; remote code does not depend on local
  config or transport. The delivery SDK is not a public Loaf control SDK. Avoid
  generic repositories, provider registries and DI containers without a concrete need.
- Keep configuration command-local; a Resources workflow may refresh its own
  settings after an explicit save. UI and flags call the same typed workflows.
  Preserve public argv, literal program arguments, JSON keys/codes, stream contracts
  and explicit zero/false/empty overrides when extending existing commands.
- Forms require explicit opt-in; `-i` never implies `--yes`. Bare root/cloud show
  offline help, and queries remain finite even with `-i`. JSON does not prompt;
  native SSH/authentication and login input keep their separate existing contracts.

## Essential invariants

- Keep Run identity and frozen binding through preparation, uncertain submission
  and observation. Remote execution facts, query failure and local-save failure
  remain distinct. Never automatically replay a possibly accepted launch, payment,
  stop or deletion. Canceling observation does not stop accepted work. Queue dispatch
  retains its fixed Run identity and separate Coordinator/Supervisor ownership;
  cancellation requires no-start, not an expired lease.
- Paid operations require explicit authority. Preserve the original request,
  account/product and instance when the outcome is uncertain; do not buy a
  replacement automatically. Acquisition stays foreground-only; it never starts
  training, releases instances or shuts machines down automatically.
- Deletion requires explicit IDs and the existing confirmation rules. `clean`
  needs a confirmed terminal run; `abandon` needs durable no-start before payload
  removal. Stop verifies the saved process identity and scope. Run cleanup must not
  reclaim shared cache, local downloads or adjacent data, or treat uncertainty as
  permission.
- Preserve verified facts when local persistence fails. State transitions and their
  events share the owning short transaction, not a network or process lifetime.
  Keep secrets/private execution values out of public output, events and source sync.
- Unknown formats fail without overwriting data. Current development does not
  migrate old stores or hot-upgrade active Supervisors; reassess the compatibility
  promise before 1.0. Do not add permanent adapters just to preserve an unreleased
  experiment, or discard the binary/data needed to manage running tasks.

## Work on the relevant path

1. Read `git status --short`, the diff, the relevant code/callers/tests and nearest
   `AGENTS.md`. Preserve unrelated work; do not reset or clean the checkout.
2. Choose the simplest useful implementation, complete its affected callers and
   examples, and update related tests. Do not expand the task into general cleanup.
3. Match verification to the change. Start with affected tests; use broader tests,
   vet, race, SSH or packaging checks when the changed contract or side effects need
   them. For prose-only edits, read the diff and check links and `git diff --check`.
4. Report the usable result, relevant checks and unresolved issues. Commit, push,
   tag, publish or use real servers/paid resources/Agent sessions only when authorized.
   Authorized commits use English `type(module): description` subjects and bodies.

Use reviewed `mise.toml`/PATH tools; check `command -v` before diagnosing a missing
runtime. Root and `sdk/agentdelivery` share `go.work`; inspect declaration changes
with `go work sync`, not unrelated standalone-module tidies. Go uses the four-space
formatter `go run ./scripts/format.go -- path.go directory/`, not standalone gofmt.
Existing entry points are `scripts/check.sh`, `scripts/integration.sh` and the sole
complete build owner `scripts/build.sh`. These are tools to choose for the task,
not mandatory steps for every edit. Exercise the current built binary when needed;
cross-compilation is not runtime evidence. Keep generated assets out of Git.

Read only the task-relevant contracts:
- CLI/config/output: [guide](manual/en/guide.md), [JSON](manual/en/json.md).
- Execution/state/transport: [remote contracts](internal/remote/AGENTS.md),
  [development](manual/en/development.md), including their client callers.
- Cloud/account/provider changes: [cloud contracts](internal/cloud/AGENTS.md),
  [cloud guide](manual/en/cloud.md), including adapters outside that directory.
- Queue/delivery: [async guide](manual/en/async.md),
  [SDK compatibility](sdk/agentdelivery/COMPATIBILITY.md).
- Packaging/upgrades: [installation](manual/en/installation.md),
  [upgrading](manual/en/upgrading.md). Do not advertise plain `go install` as a
  complete build.

Keep READMEs, bilingual manuals and the standalone Skill aligned with changed user
behavior; update both locale catalogs for user-visible copy. Grammar changes use
`LOAF_UPDATE_MANUAL=1 go test ./internal/cli -run TestCommandReferenceAndExamples`.
`docs/` is ignored local work: do not force-add it or make public builds depend on
it. Do not add governance/release systems as incidental feature work.
