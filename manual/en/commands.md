# Commands and minimal calls

[Home](../../README.md) · [Guide](guide.md) · [简体中文](../zh-Hans/commands.md)

Generated from the CLI grammar. NAME, IDs, hosts and environment references in examples are placeholders. Cloud calls require a configured, applicable account and resource conditions; mutation still requires explicit authority and confirmation. See [cloud prerequisites](cloud.md). Add `--json` for noninteractive structured results, except native `ssh`, `forward` and `notify channel`. Use an explicit run ID for agent follow-ups.

## Global options

| Option | Type | Purpose |
| --- | --- | --- |
| `--lang` | `string` | Interface language: auto, en, or zh-Hans. |
| `--json` | `bool` | Print JSON; exec, logs -f, cloud watch and continuous notify serve stream NDJSON. |
| `--trace` | `bool` | Report safe SSH-stage timings and connection/retry counts for a finite command. |
| `--target` | `string` | Override the project's target alias. |
| `--help` | `bool` | Show help. |
| `--version` | `bool` | Show the build version. |

## Experiments and project files

| Command | Minimal call | Purpose |
| --- | --- | --- |
| `loaf run` | `loaf run -- python3 train.py` | Sync and start a durable background task. |
| `loaf status` | `loaf status` | Query a run or list recent runs in this project. |
| `loaf logs` | `loaf logs` | Read a bounded log tail or follow one run. |
| `loaf stop` | `loaf stop` | Stop a saved run and wait for its confirmed result. |
| `loaf inspect` | `loaf inspect r_example` | Inspect saved task details offline. |
| `loaf outputs` | `loaf outputs` | List a task's output files. |
| `loaf wait` | `loaf wait --tag experiment` | Wait for selected tasks to finish. |
| `loaf pull` | `loaf pull` | Download selected outputs from a saved run. |
| `loaf clean` | `loaf clean r_example --dry-run` | Remove completed tasks' remote data. |
| `loaf sync` | `loaf sync --dry-run` | Synchronize working sources to the managed remote cache. |
| `loaf queue` | `loaf queue` | Queue work on one machine with optional FIFO dispatch. |
| `loaf queue list` | `loaf queue list` | List the selected machine’s queue and shared capacity. |
| `loaf queue enable` | `loaf queue enable --slots 1` | Explicitly enable FIFO dispatch with fixed concurrency. |
| `loaf queue submit` | `loaf queue submit -- python3 train.py` | Freeze project input and enqueue one background task. |
| `loaf queue show` | `loaf queue show q_example` | Inspect a saved request and its Run identity. |
| `loaf queue cancel` | `loaf queue cancel q_example` | Cancel a request that has not entered dispatch. |
| `loaf queue pause` | `loaf queue pause` | Pause new dispatch; running tasks continue. |
| `loaf queue resume` | `loaf queue resume` | Resume dispatch from the saved queue. |
| `loaf queue clean` | `loaf queue clean q_example --dry-run` | Delete prepared inputs for explicitly canceled requests. |

## Machines and foreground operations

| Command | Minimal call | Purpose |
| --- | --- | --- |
| `loaf machine` | `loaf machine` | Manage saved machines and the current project selection. |
| `loaf machine list` | `loaf machine list` | List saved machines. |
| `loaf machine show` | `loaf machine show lab` | Show a saved machine's configuration. |
| `loaf machine add` | `loaf machine add lab --ssh lab-gpu` | Save an SSH machine alias. |
| `loaf machine use` | `loaf machine use lab` | Select the default machine for this project. |
| `loaf machine remove` | `loaf machine remove lab` | Remove a local machine alias, preserving runs and remote files. |
| `loaf exec` | `loaf exec -- nvidia-smi` | Run a foreground command on the selected machine. |
| `loaf ssh` | `loaf ssh` | Open the selected machine's native interactive SSH shell. |
| `loaf forward` | `loaf forward 6006` | Forward a local loopback port over a foreground SSH connection. |

## Cloud resources

| Command | Minimal call | Purpose |
| --- | --- | --- |
| `loaf cloud` | `loaf cloud --help` | Manage cloud resources and connect machines. |
| `loaf cloud login` | `loaf cloud login main` | Log in with an AutoDL main or subaccount password |
| `loaf cloud logout` | `loaf cloud logout main` | Clear the selected saved session and login digest |
| `loaf cloud list` | `loaf cloud list` | List this account’s instances with bounded pagination |
| `loaf cloud info` | `loaf cloud info instance-id` | Inspect an instance without exposing secrets |
| `loaf cloud credentials` | `loaf cloud credentials instance-id` | Fetch current SSH connection details; redact passwords by default |
| `loaf cloud start` | `loaf cloud start instance-id` | Start an existing whole instance after explicit approval |
| `loaf cloud stop` | `loaf cloud stop instance-id` | Stop a whole instance, not just a Loaf experiment |
| `loaf cloud search` | `loaf cloud search` | Read inventory once and filter/rank matching offers |
| `loaf cloud watch` | `loaf cloud watch --wait 10m` | Monitor inventory in the foreground without renting |
| `loaf cloud acquire` | `loaf cloud acquire gpu` | Acquire at most one instance and bind its SSH target |
| `loaf cloud bind` | `loaf cloud bind gpu --instance instance-id` | Bind an existing instance and verify SSH authentication |
| `loaf cloud requests` | `loaf cloud requests` | Inspect local allocation and power-operation receipts |
| `loaf cloud reconcile` | `loaf cloud reconcile request-id --instance instance-id` | Resolve an uncertain request without replaying its mutation |
| `loaf cloud account` | `loaf cloud account` | Manage user-owned account credential references |
| `loaf cloud account list` | `loaf cloud account list` | List account references without reading tokens |
| `loaf cloud account add` | `loaf cloud account add main --token-env AUTODL_TOKEN` | Save a platform product and token environment-variable reference |
| `loaf cloud account remove` | `loaf cloud account remove main` | Remove an account alias, keeping frozen instance bindings |

## Diagnostics and help

| Command | Minimal call | Purpose |
| --- | --- | --- |
| `loaf check` | `loaf check` | Inspect connectivity and the remote environment. |
| `loaf version` | `loaf version` | Show build and protocol versions, source revision, and platform. |
| `loaf events` | `loaf events` | Read saved run, queue and resource facts. |
| `loaf events list` | `loaf events list` | Read a bounded event page with an optional cursor. |
| `loaf notify` | `loaf notify` | Manage explicit destinations and event delivery. |
| `loaf notify delivery` | `loaf notify delivery --help` | Inspect and recover one delivery |
| `loaf notify delivery show` | `loaf notify delivery show d_example` | Inspect evidence, attempts and lineage |
| `loaf notify delivery reconcile` | `loaf notify delivery reconcile d_example` | Query explicit acknowledgements without resending |
| `loaf notify delivery resolve` | `loaf notify delivery resolve d_example --as abandoned` | Record a user confirmation or abandonment |
| `loaf notify delivery resend` | `loaf notify delivery resend d_example --allow-duplicate --yes` | Queue one explicitly authorized successor |
| `loaf notify subscription` | `loaf notify subscription --help` | Manage one notification intent |
| `loaf notify subscription show` | `loaf notify subscription show sub_example` | Show a subscription and its sources |
| `loaf notify subscription disable` | `loaf notify subscription disable sub_example` | Pause collection and new sends |
| `loaf notify subscription enable` | `loaf notify subscription enable sub_example` | Resume from saved cursors |
| `loaf notify subscription remove` | `loaf notify subscription remove sub_example` | Archive a subscription and close unsent work |
| `loaf notify prune` | `loaf notify prune --before 2026-01-01T00:00:00Z --dry-run` | Prune settled local notification history |
| `loaf notify bind` | `loaf notify bind here --current` | Verify and bind the current experimental Codex desktop chat |
| `loaf notify destination` | `loaf notify destination --help` | Manage private bindings to exact destinations. |
| `loaf notify destination list` | `loaf notify destination list` | List destinations without private connection details. |
| `loaf notify destination remove` | `loaf notify destination remove research` | Remove an unreferenced destination |
| `loaf notify destination add` | `loaf notify destination add research --kind codex --session thread-id` | Save an explicit Agent session or Webhook destination. |
| `loaf notify subscribe` | `loaf notify subscribe run:r_example --destination research` | Subscribe one subject to named destinations. |
| `loaf notify subscriptions` | `loaf notify subscriptions` | List durable notification subscriptions. |
| `loaf notify deliveries` | `loaf notify deliveries` | Inspect acceptance and processing evidence. |
| `loaf notify serve` | `loaf notify serve --once` | Collect and deliver events; Ctrl-C stops only this Bridge. |
| `loaf notify channel` | `loaf notify channel --socket /private/channel.sock --session research` | Serve a Claude Channel on native MCP stdio; requires an open, platform-permitted session. |
| `loaf help` | `loaf help run` | Show help for a command. |

## Parameters and constraints

Use `loaf help COMMAND` for parameter descriptions, built-in defaults, ranges and examples; `loaf help COMMAND --json` adds machine-readable argument and flag metadata. The same declarations produce this list and the help interface. [Argument construction](guide.md#construct-arguments-without-guessing) covers literal argv, repeated flags, CSV, false/zero/empty values and Shell boundaries. [Configuration](configuration.md) explains effective precedence. [JSON](json.md) explains result types and errors.

`status` with no ID lists recent tasks; `logs`, `stop`, `outputs` and `pull` select the latest submitted or uncertain task when their ID is omitted. `wait` needs explicit IDs or tags; `clean` always needs explicit IDs. Additional common calls:

```bash
loaf run
loaf status ID
loaf logs ID -f
loaf wait ID1 ID2
loaf pull ID1 ID2 --into ./comparison
loaf check --targets a,b
loaf cloud acquire gpu-copy --clone-from old-gpu
loaf cloud bind gpu --request REQUEST
loaf cloud requests REQUEST
```
