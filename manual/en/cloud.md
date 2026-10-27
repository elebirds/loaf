# Use AutoDL resources explicitly

[Home](../../README.md) · [Command reference](commands.md) · [简体中文](../zh-Hans/cloud.md)

## Acquire and connect AutoDL compute

Existing SSH targets need no cloud account. For AutoDL, log in with a main account's phone number or the full `subuser@master-id` login. One verified session can access **ordinary instances** (`standard`) and **Pro** (`pro`), subject to the account's permissions. Password input is hidden; credentials are never accepted as command-line flags.

```bash
loaf cloud login autodl-main --username 'your-subuser@master-id' --default
loaf cloud list --account autodl-main
loaf cloud list --account autodl-main --product pro
```

### Choose where to save credentials

```bash
loaf cloud login autodl-main --credential-store keyring   # Default
loaf cloud login autodl-main --credential-store file      # Unencrypted private files
```

`keyring` uses macOS Keychain or Linux Secret Service (requires a working user session). `file` needs no vault service: entries are stored under `$XDG_STATE_HOME/loaf/credentials/`, defaulting to `~/.local/state/loaf/credentials/`, with directory mode `0700` and file mode `0600`. File storage protects against other OS users, not software running as you. Keyring failures never silently fall back to files. The CLI supports macOS/Linux; Windows users currently need WSL.

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

`credentials` shows the current SSH endpoint, redacting its password. Only explicit `--show-password` reveals it. Establish first-host trust with native SSH; noninteractive commands never approve a new or changed host key. Cloud connections prefer keys and can use a fetched password through native OpenSSH askpass. Passwords stay in memory behind a temporary private local socket, not in configuration, subprocess arguments or environment variables. The short-lived launcher contains no password.

Cloud runner operations share one private, command-scoped OpenSSH master per bound instance and connection configuration. Connection details are resolved once while that master is healthy; its authenticated connection carries probes, runner requests, and native rsync. Loaf never borrows a pre-existing master or writes SSH configuration. Normal exit and cancellation close only this command's masters; a parent-lifetime pipe also prevents an orphaned persistent connection if the CLI is killed. Static SSH targets and foreground `exec`, `ssh`, and `forward` retain their original connection behavior.

`bind` checks that the account can inspect this instance, saves the binding, then verifies authenticated SSH. `ready` means platform running + SSH authenticated + target saved, not Python/CUDA ready. `--ready-timeout` defaults to 2m (0s makes one bounded check). A created but unreachable instance may already be billing: keep its ID and fix its connection, rather than renting another. Retry a saved acquisition with `loaf cloud bind gpu-a --request <request-id>`; it never creates a replacement. An explicit `--replace` can rebind an alias; older runs still refresh their original instance's endpoint.

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

Other conditions are `--gpu-vendor`, `--gpu-model`, `--min-cpus`, `--min-disk` and `--region`. Models and regions are exact platform labels/IDs; inspect search output rather than guessing them. Memory/disk CLI floors require the `GiB` suffix. Ordinary data-disk expansion uses `--expand-data-disk`; Pro system expansion uses `--expand-system-disk` (both use the platform's GB convention). Image pullability and dependency readiness are not verified before rental. Image settings are product-specific: `--image` selects an ordinary base-image UUID or a Pro image UUID. For a private image with either web-session product, use `--private-image` instead of `--image`, not both. Developer Pro uses its own image UUID.

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

Data-disk expansion defaults to the source allocation; project/user settings and `--expand-data-disk` may increase it, not shrink it. Compute conditions and spending ceilings retain their normal precedence. With no wait preference, clone acquisition defaults to 30m; project/user settings or `--wait` override it, including explicit `0s` for one attempt. The maximum window is 24h, and the command must remain running to keep acquiring.

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

Polling cycles start at 10s, respect a configurable 10s–10m minimum (creation adds one immediate recheck), and add backoff/jitter and `Retry-After` on transient failures. Waiting is bounded to 24h; closing the client ends monitoring, not a resource already created. Human output reports matches, rejection reasons, coverage and observation time. JSON finite commands/acquire return one envelope; `watch --json` uses NDJSON change events, sparse heartbeats and a final end/error. Changed configuration cannot broaden an already running authorization.

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

This is not read-only stock monitoring. `--allow-unquoted` cannot bypass an effective price ceiling; that case returns `PRICE_UNVERIFIABLE` without charging. Unverifiable generic hardware floors are also refused, not silently relaxed. Pro region IDs are data-center IDs, unlike ordinary region-sign IDs. `118` is the platform's CUDA 11.8 lower-bound code. Loaf never uses a platform startup command to run experiments. See the [official Pro API](https://www.autodl.com/docs/instance_pro_api/), the [current ordinary rental form](https://www.autodl.com/assets/index.9040cb9d.js), and the [GET-query observation](https://github.com/RordChang/autodl-pro-codex-skill/blob/main/references/api.md). Developer Pro snapshot/status use query parameters; no automatic GET-body fallback or mutation replay is attempted.

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
