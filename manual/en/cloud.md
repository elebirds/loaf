# Use AutoDL resources explicitly

[Home](../../README.md) · [Command reference](commands.md) · [简体中文](../zh-Hans/cloud.md)

## Choose the operation

Use direct commands to manage resources:

```bash
loaf cloud login --prompt autodl-main
loaf cloud list --account autodl-main
loaf cloud info INSTANCE_ID --account autodl-main
loaf cloud acquire -i
```

GPU acquisition has a dedicated interactive flow for comparing candidates and
reviewing costs. Other operations use their command arguments and, when needed,
a single confirmation. Bare `loaf cloud`, with or without `-i`, shows offline
help. Use `--json` for automation; it cannot combine with `-i`.

## Rent a GPU in one guided flow

```bash
loaf cloud acquire -i
```

With `-i`, the Huh wizard collects four groups of choices:

1. **Compute:** search the actual GPU model names, choose the card count and allowed regions. Resource floors are optional advanced conditions.
2. **Selection or automatic acquisition:** select a currently available host, or set a whole-instance hourly price cap and wait for matching stock.
3. **Environment and storage:** select a base or personal image by its framework/Python/CUDA label, then choose disk expansion. Expanded storage has a separate daily price cap.
4. **Review:** check the account, product, image, prices and conditions; choose an alias. Type `yes` to authorize at most one instance.

Regions are grouped by the platform's data center, with one choice per display area. Selecting an area includes all of its internal region IDs. A flag or profile restricted to particular IDs keeps that exact scope; only explicitly selecting the whole group expands it. `--region` and catalog JSON retain canonical IDs.

With no region preference, the wizard preselects regions marked public by the platform. Enterprise regions remain visible and can be checked as needed. Existing settings take precedence, including an explicit empty list. Clearing all choices allows any region, including enterprise regions. Noninteractive commands keep their original semantics: no configured region condition means unrestricted, without the wizard's preselection.

The header keeps the complete route visible: **Mode → GPUs → Host/conditions → Image/disk → Approve → Acquire → Connect**. `●` marks the current step, `✓` marks earlier completed choices, and `○` marks upcoming steps. Only the current step's fields expand; going back updates the position.

Use arrows to navigate, `/` to search, Space to toggle multiple choices, Enter to continue, and Shift+Tab to move between fields. Each page offers back/cancel navigation where choices change. Ctrl+C cancels. A plain-terminal fallback uses numbered choices; Enter keeps the marked choice, comma-separated numbers select multiple entries, and `0` clears them. Existing flags and profiles prefill the wizard. Ordinary complete commands run directly; with `-i`, configured inputs open at review. `--yes` skips the wizard. Without `-i`, missing inputs produce diagnostics instead. A single saved account can be selected for this invocation without changing its global default. With no account, the wizard can return from native login to acquisition.

A complete profile opens at review. Go back to change individual groups without losing the draft. Review can display an equivalent command without payment approval. Save reusable preferences with `cloud profile save`.

**Selection fixes one candidate.** The review requotes that candidate with the chosen environment and storage. If stock or price changes, return to selection; Loaf never silently buys a different host. For web Pro, a candidate is a model/region/specification combination, not a physical host reservation. The first guided path supports pay-as-you-go, base/personal images; period billing, coupons and community-application creation are not offered.

Compare price, GPU/VRAM, CPU, RAM, region and ID. Price ascending is the default; CPU/RAM descending put unknown values last. Refresh or load the next window. Standard scans stay within `--limit` per round and continue through later pages before wrapping. Windows collected at different times are not a complete snapshot. Later-page selections recheck the same ID in the same window; an exported `loaf-offer-v1:…` value is a discovery hint, not a reservation. Web Pro retains aggregate inventory semantics.

**Automatic acquisition freezes conditions before waiting.** After authorization it polls and automatically requests a matching instance, without another purchase prompt. The foreground display shows the stage, elapsed time, next actual poll, poll/allocation counts and inventory coverage. The main view highlights the blocker and checked range; Tab shows technical details. Before allocation, press `e` to stop waiting and edit, then authorize again. Created or uncertain results only offer recovery of the original resource. Starting, disk-copying and SSH connection stages remain distinct. There is no estimated percentage for unknown stock arrival. The display's clock does not poll the provider.

Forms and progress update in place, showing only the current stage. Inventory statistics disappear when SSH connection begins. Review the model, budget, environment and billing notice together on the approval page; technical IDs and rejection reasons stay in progress details. Completion leaves one result and a next-step command. If SSH is unconfirmed, that result retains the original instance and gives the command to resume its connection.

`--continuous` explicitly waits until success or cancellation; `--wait 30m` is bounded, and `--wait 0s` still means one attempt. Do not combine `--continuous` with explicit `--wait`, or `--offer` with either monitoring mode. The client must remain running. Cancellation never shuts down an already created instance. New continuous/fixed-offer requests require a compute price cap; paid disk expansion also requires `--max-storage-daily-price`. Neither is a total-spend limit.

### Discover choices and reuse preferences

```bash
loaf cloud catalog gpu --query 4090
loaf cloud catalog region
loaf cloud catalog image --query PyTorch
loaf cloud catalog image --kind private
loaf cloud account use autodl-main
loaf cloud profile list
loaf cloud profile show train
loaf cloud profile save train --gpu-model 'RTX 4090' --image IMAGE --max-compute-hourly-price 3
loaf cloud profile remove train --yes
```

Directories return canonical values, not live stock. `--query` searches names/IDs; `--limit` bounds returned items and coverage remains visible. Standard regions and Pro data centers are different values. The Pro region directory reports the known console subset as incomplete; developer-token Pro directories are unsupported. The wizard uses searchable valid image combinations rather than making up versions. Image metadata compatibility is checked for new fixed/continuous requests, not package installation or experiment dependencies.

`profile save` stores effective preferences offline using existing precedence, even before renting; replacing a personal name needs `--replace`. `remove` deletes only the personal profile, with interactive confirmation or batch `--yes`. A same-named project profile remains effective. Profile views report sources and overrides without editing project files.

### Use the same workflow without prompts

```bash
# Offline local validation: no credentials, network, IDs, state or reservation.
loaf cloud acquire gpu-a --profile train --dry-run --json

# IMAGE is a canonical image ID from the appropriate directory.
loaf cloud acquire gpu-a --account autodl-main \
  --gpu-model 'RTX 4090' --image IMAGE \
  --max-compute-hourly-price 2.50 --continuous --yes --json

# Pin an offer returned by search; never substitute a cheaper candidate.
loaf cloud acquire gpu-a --account autodl-main --offer OFFER_ID \
  --image IMAGE --max-compute-hourly-price 2.50 --yes --json
```

Dry-run reports effective values, per-field sources, pending live checks and canonical round-trippable argv in `next_actions`, without credentials or `--yes`. Explicit zero, empty lists and false are preserved. Clone previews resolve a saved source alias locally; source image/region/disk inheritance remains pending live validation. A successful preview does not prove stock, a quote, clone readiness or SSH access. It is optional, not a prerequisite for acquisition.

Finite JSON still has one stdout result. Default `--json` suppresses progress; opt in with `--progress=json` for versioned NDJSON on **stderr**. `--progress=plain` writes sparse stage updates and `none` suppresses runtime feedback. Cloud results add `context` and argv-style `next_actions` without replacing stage-specific `data`. Follow known request/instance IDs on failure:

```bash
loaf cloud bind --request REQUEST_ID --json
```

This recovers the alias and frozen resource from the receipt, never rents again. `cloud info`, `cloud start`, `cloud stop` and `cloud bind` require an exact instance ID, bound alias or saved request; they do not open an instance-management menu. Whole-instance power changes still require explicit confirmation.

## Acquire and connect AutoDL compute

Existing SSH targets need no cloud account. For AutoDL, log in with a main account's phone number or the full `subuser@master-id` login. One verified session can access **ordinary instances** (`standard`) and **Pro** (`pro`), subject to the account's permissions. Password input is hidden; credentials are never accepted as command-line flags.

```bash
loaf cloud login --prompt autodl-main --username 'your-subuser@master-id' --default
loaf cloud list --account autodl-main
loaf cloud list --account autodl-main --product pro
```

For native username/password input, explicitly use `--prompt` or `-i`; `--verify` explicitly requests verification-code input. Without these options, provide inputs such as `--username` and `--password-stdin`, or reuse saved credentials. Authentication stays in one native input session.

### Choose where to save credentials

```bash
loaf cloud login --prompt autodl-main --credential-store keyring   # Default
loaf cloud login --prompt autodl-main --credential-store file      # Unencrypted private files
```

`keyring` uses macOS Keychain or Linux Secret Service (requires a working user session). `file` needs no vault service: entries are stored under `$XDG_STATE_HOME/loaf/credentials/`, defaulting to `~/.local/state/loaf/credentials/`, with directory mode `0700` and file mode `0600`. File storage protects against other OS users, not software running as you. Keyring failures never silently fall back to files. See [installation](installation.md) for native Windows requirements; native Windows keyring acceptance is still pending.

Set a default for **new logins** in the user configuration, not project `loaf.toml`:

```toml
[cloud]
credential_store = "file"    # Or "keyring"
```

An existing account keeps its saved store unless `--credential-store` explicitly changes it. A switch or account replacement retains earlier credential entries for historical run bindings; it does not rewrite their access. `cloud logout <name>` deletes only the currently selected entry. `cloud account remove <name>` removes only the alias/default, not credentials or cloud resources. Revoke sessions on the platform to invalidate all copies.

Loaf saves the session token and, by default, AutoDL's replayable SHA-1 password digest—not the original password. **The digest is a password equivalent**, including in file mode. `--remember-password=false` saves only the session. When it expires, `cloud login autodl-main` reauthenticates using the saved digest; `--prompt` forces fresh password entry. A changed verified principal requires `--replace` and receives a new credential reference. Failed authentication leaves the existing saved login intact.

Main-account SMS/CAPTCHA challenges return `AUTH_CHALLENGE_REQUIRED`; complete verification through AutoDL, then use `cloud login autodl-main --verify` for an obtained SMS code. Loaf does not solve CAPTCHA or automatically replay password requests. Noninteractive callers may use `--username ... --password-stdin`; `--json` never prompts. Do not put passwords in shell history or chat.

For automation, explicit environment-token references remain available:

```bash
loaf cloud account add autodl-pro --product pro --auth developer \
  --token-env AUTODL_PRO_TOKEN
loaf cloud account add autodl-session --product standard --auth session \
  --token-env AUTODL_WEB_TOKEN
loaf cloud account list --json
```

Tokens are read from the calling environment; TOML contains only their variable names. Developer tokens use the official Pro control plane and cannot access ordinary instances. Session tokens use the web control plane; saved native logins verify main/sub identity and permissions. `--app-version` can explicitly supply the web console header. No token scraping or automatic developer-token creation is performed.

`cloud login --product pro` sets an account's default product. Per operation, `--product` overrides the resource profile's `product`, then that default. Bound aliases keep their saved product and reject conflicting overrides. Authentication mode and product selection never silently switch API hosts or credentials.

### Connect an existing instance

```bash
loaf cloud credentials <instance-id> --account autodl-main
loaf cloud bind gpu-a --instance <instance-id> --account autodl-main
loaf machine use gpu-a
loaf run -- python3 train.py
```

`credentials` shows the current SSH endpoint, redacting its password. Only explicit `--show-password` reveals it. Provider-backed connections default to OpenSSH `accept-new`: the original command, including JSON, accepts and records a new host key without a separate SSH session or prompt. Changed known keys are rejected; explicit per-target host-key settings take precedence. Cloud connections prefer keys and can use a fetched password through native OpenSSH askpass. Passwords stay in memory behind a temporary private local socket, not in configuration, subprocess arguments or environment variables. The short-lived launcher contains no password.

Cloud runner operations share one private, command-scoped OpenSSH master per bound instance and connection configuration. Connection details are resolved once while that master is healthy; its authenticated connection carries probes, runner requests, and native rsync. Loaf never borrows a pre-existing master or writes SSH configuration. Normal exit and cancellation close only this command's masters; a parent-lifetime pipe also prevents an orphaned persistent connection if the CLI is killed. This private-master policy does not change connection ownership for static targets or foreground `exec`, `ssh`, and `forward`.

`bind` checks that the account can inspect this instance, saves the binding, then verifies authenticated SSH. `ready` means platform running + SSH authenticated + target saved, not Python/CUDA ready. `acquire/bind --ready-timeout` defaults to 5m, capped at 10m; 0s makes one bounded check. A created but unreachable instance may already be billing: keep its ID and fix its connection, rather than renting another. Retry a saved acquisition with `loaf cloud bind --request <request-id>`; it never creates a replacement. An explicit `--replace` can rebind an alias; older runs still refresh their original instance's endpoint.

`ready` does not activate the image's Python environment. If a confirmed command
failure shows that the non-login Supervisor cannot find `python3`, use that
image's actual interpreter on the same target, for example
`loaf run --target gpu-a -- /root/miniconda3/bin/python3 train.py` when that path
has been verified. Login-shell PATH can differ and interpreter paths vary by
image. An unconfirmed Run still needs observation of its original ID.


Connection readiness runs only fixed `/bin/true` probes, at most ten seconds each. Transient connection and platform-read failures retry every five seconds. Trust, authentication and configuration failures immediately give a safe classification and action. Probe counts are separate from allocation attempts; ordinary foreground `exec` is never replayed.

After fixing a connection failure, retry `cloud bind` for the same instance or
saved request. First-use trust needs no separate step. A changed host key requires
manual investigation; Loaf never removes the old key automatically. Rebinding the
same frozen identity is idempotent; a different resource still needs explicit `--replace`.

### Choose conditions in configuration or directly

An optional project `loaf.toml` profile describes the experiment's resource needs, not credentials:

```toml
[resources.train]
gpu_vendor = "nvidia"
gpu_count = 1
min_gpu_memory_gib = 24       # Per card, not the total across GPUs
min_memory_gib = 32
max_compute_hourly_price = "2.50"
currency = "CNY"
wait_timeout = "30m"
poll_interval = "10s"

[resources.train.autodl]
image = "your-image-uuid"
```

Ordinary and session-backed Pro requests use the platform's price-preview API. Budgets use exact decimal CNY and the **whole instance's hourly compute fee**; separately quoted storage is daily. Loaf does not guess units from raw catalog prices or require users to declare them. A new quote is checked immediately before creation. An hourly cap is neither a total-spend limit nor a server-enforced price lock; the platform can still change between quote and allocation.

```bash
loaf cloud search --account autodl-main --profile train
loaf cloud watch --account autodl-main --profile train
loaf cloud acquire gpu-a --account autodl-main --profile train --yes
loaf machine use gpu-a
loaf run -- python3 train.py
loaf logs
loaf pull
```

No profile is required:

```bash
loaf cloud acquire gpu-a --account autodl-main \
  --gpu-count 1 --min-gpu-memory 24GiB --min-memory 32GiB \
  --max-compute-hourly-price 2.50 --currency CNY \
  --image your-image-uuid --wait 30m --yes
```

Other conditions are `--gpu-vendor`, `--gpu-model`, `--min-cpus`, `--min-disk` and `--region`. Models and regions are exact platform labels/IDs; use `cloud catalog` or the wizard rather than guessing them. Memory/disk CLI floors require the `GiB` suffix. Ordinary data-disk expansion uses `--expand-data-disk`; Pro system expansion uses `--expand-system-disk` (both use the platform's GB convention). Image pullability and dependency readiness are not verified before rental. Image settings are product-specific: `--image` selects an ordinary base-image UUID or a Pro image UUID. For a private image with either web-session product, use `--private-image` instead of `--image`, not both. Developer Pro uses its own image UUID.

Precedence is explicit flags, project profile, user profile, then defaults, **independently per field**. `resources.default` supplies optional shared defaults; a named profile overlays it. User `[cloud.defaults]` supplies lower-priority personal defaults. Model/region lists replace the whole lower-priority list, rather than widening it. Explicit zero/empty overrides are preserved when valid. Accounts and `cloud.default_account` are user-only; project files cannot contain them.

A separate user safety ceiling cannot be raised or cleared by project settings or CLI flags:

```toml
[cloud.safety]
max_compute_hourly_price = "3.00"
currency = "CNY"
```

### Keep your environment and data when moving to another GPU

When an existing machine has no free GPU, clone it onto an available machine in the **same region**:

```bash
loaf cloud acquire gpu-copy --account autodl-main --product standard \
  --clone-from old-gpu --gpu-model 'RTX 5090' --gpu-count 1 \
  --max-compute-hourly-price 3.00 --wait 24h --ready-timeout 10m --yes
```

`--clone-from` accepts an instance ID or a bound cloud alias. Ordinary AutoDL instances support cloning with main/subaccount sessions; Pro/developer-token cloning is not supported. The source must already be shut down and not cloning. Loaf retains it and never stops it automatically.

The existing acquisition loop waits for eligible stock, rechecks the winner and price, then requests **both system- and data-disk copying**. It never falls back to another region or a blank instance. Conflicting region settings are rejected. Source vendor, architecture and CUDA requirements constrain image compatibility. The clone inherits the source image, ignoring configured image defaults; explicit image/spec/CUDA/system-disk flags and `--allow-unquoted` are rejected.

Data-disk expansion defaults to the source allocation; project/user settings and `--expand-data-disk` may increase it, not shrink it. Compute conditions and spending ceilings retain their normal precedence. With no wait preference, clone acquisition defaults to 30m; project/user settings or `--wait` override it, including explicit `0s` for one attempt. Finite windows are at most 24h; explicit `--continuous` waits until canceled or successful. The command must remain running to keep acquiring.

SSH becoming reachable does not mean copying has finished. Clone `ready` also requires the platform's successful data-copy result. If the observation timeout expires, retain the returned request/instance IDs and resume the same clone:

```bash
loaf cloud info gpu-copy --json
loaf cloud bind gpu-copy --request REQUEST_ID --ready-timeout 10m
```

`clone_data_status` reports `copying`, `succeeded`, `failed` or `unknown` when provided by the platform. `CLONE_FAILED` retains the instance for recovery; neither a timeout nor a copy failure rents a replacement. The receipt freezes the source identity and region, including across process restarts and alias/account changes.

### Monitoring is not a paid allocation

| Operation | Behavior |
| --- | --- |
| `cloud search` | One read-only bounded inventory query; filters unknown required fields out and ranks eligible offers by price. |
| `cloud watch` | Foreground read-only monitoring; requires a positive `--wait`/profile timeout. Never rents. |
| `cloud acquire <alias>` | Explicit authorization for at most one instance; rechecks conditions, records intent before creation, then connects it. |

Polling cycles start at 10s, respect a configurable 10s–10m minimum (creation adds one immediate recheck), and add backoff/jitter and `Retry-After` on transient failures. Finite waiting is bounded to 24h; acquisition can explicitly use `--continuous`. Closing the client ends monitoring, not a resource already created. Human output reports matches, rejection reasons, coverage and observation time. JSON finite commands/acquire return one envelope; `watch --json` uses NDJSON change events, sparse heartbeats and a final end/error. Changed configuration cannot broaden an already running authorization.

Ordinary inventory is an **unofficial web-session API**. Hardware units are normalized from the platform's public rental form: GPU memory is per card; CPU/RAM allocations scale by card count. Offline machines and exhausted instance slots are excluded. Missing required fields stay unknown. The adapter examines at most `--limit` entries (100 by default, up to 1000); incomplete pagination remains visible and “cheapest” means cheapest within that examined set.

Session-backed Pro supports `search` and `watch` using GPU catalogs, numeric spec IDs and per-request quotes:

```bash
loaf cloud search --account autodl-main --product pro --region westDC3
loaf cloud acquire pro-a --account autodl-main --product pro \
  --gpu-model 'platform-model-name' --region westDC3 \
  --image your-base-image-uuid --cuda-from 118 \
  --max-compute-hourly-price 3.00 --wait 10m --yes
```

Without `--region`, web Pro searches the console's supported `westDC3` and `beijingDC2` data centers separately. `--gpu-spec-id` optionally fixes a numeric web spec; otherwise matching selects it. It is distinct from developer `--gpu-spec` (UUID). Catalog stock is aggregate model/region availability, not reserved capacity for a spec. CPU/RAM ranges are conservative floors, not promised final allocations; fields the catalog does not verify, including GPU memory/vendor, remain unknown and cannot satisfy a requested floor. The selected region/model/spec is rechecked, and allocation can still return no stock. Neither product substitutes Elastic inventory for Pro.

Pro's developer API supports create/list/info/start/stop/connection, but has no verified read-only stock or pre-allocation price capability in this adapter. `search`/`watch` return `UNSUPPORTED_CAPABILITY`. An explicit Pro spec can instead use **authorized allocation attempts**, retrying only a narrowly mapped, confirmed no-stock rejection:

```bash
loaf cloud acquire pro-a --account autodl-pro \
  --gpu-spec your-spec-uuid --image your-image-uuid --cuda-from 118 \
  --gpu-count 1 --wait 10m --allow-unquoted --yes
```

This is not read-only stock monitoring. `--allow-unquoted` cannot bypass an effective price ceiling; that case returns `PRICE_UNVERIFIABLE` without charging. Unverifiable generic hardware floors are also refused, not silently relaxed. Pro region IDs are data-center IDs, unlike ordinary region-sign IDs. `118` is the platform's CUDA 11.8 lower-bound code. Loaf never uses a platform startup command to run experiments. See the [official Pro API](https://www.autodl.com/docs/instance_pro_api/), the [current ordinary rental form](https://www.autodl.com/create), and the [GET-query observation](https://github.com/RordChang/autodl-pro-codex-skill/blob/main/references/api.md). Developer Pro snapshot/status use query parameters; no automatic GET-body fallback or mutation replay is attempted.

### Recover uncertainty and control billing explicitly

```bash
loaf cloud requests --json
loaf cloud requests <request-id> --json
loaf cloud reconcile <request-id> --instance <known-instance-id> --json
loaf cloud bind gpu-a --request <request-id>
loaf cloud start gpu-a --wait 2m --yes
loaf cloud stop gpu-a --wait 2m --yes
```

Receipts are private rows in the local SQLite database, committed before paid or power requests. Network loss, cancellation during creation, invalid responses and ambiguous server failures return `CLOUD_UNCONFIRMED` (exit 3), retaining the request ID and any known instance ID. Loaf never retries such a mutation or automatically rents a replacement; unresolved allocation intents block further acquisitions on that account. Only confirmed mapped no-stock failures can trigger another sequential attempt within the original approval window.

For an unknown creation, check the platform console and reconcile its known ID. If a human has verified that **no instance was created**, `cloud reconcile <request-id> --no-instance --yes` clears the guard. This assertion is not an automatic inference from a bounded list. Power-operation receipts reconcile by querying the same saved instance; reconciliation never repeats a power request.

`cloud start/stop` affect the **entire instance**, unlike `loaf stop`, which stops one experiment. An accepted power request is not completion; use `--wait` or `info` to observe it. Stopping an instance can affect other users/jobs, and storage may keep billing afterward. Loaf does not release instances, shut them down after training, or impose a runtime/spend limit. Renting does not start training, and `run` never rents implicitly.
