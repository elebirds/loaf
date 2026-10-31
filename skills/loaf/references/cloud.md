# Authorized cloud operations

Use existing SSH targets without cloud configuration. When cloud operations are
authorized, choose the configured account and product. The adapters support a
verified main/sub session for ordinary AutoDL and Pro, plus a separate
developer-token Pro adapter; unsupported combinations do not switch control planes.
An account's default product is a preference. `--product` overrides profile
`product`, then that default, but cannot redirect an already bound alias.

## Accounts and login

Reuse saved credential references. For a first login, hand the human a native
terminal command with explicit input permission:

```bash
loaf cloud login autodl-main -i
```

`--prompt` also opts into native username/password input; `--verify` requests the
verification step. Choose `--credential-store keyring` or explicit unencrypted
`file`; new-login defaults belong in user `[cloud] credential_store`, not project
TOML. Login remembers a replayable password digest unless disabled. Treat it as a
password; never request it in chat or extract browser tokens, credential files or
training environments. `cloud login NAME` without new input can explicitly
reauthenticate with a saved digest; do not repeat password failures automatically
or bypass verification. Logout deletes only the selected credential entry.

## Acquire with the needed conditions

Discover only missing values; these are alternatives, not a preflight checklist:

```bash
loaf cloud catalog gpu --account autodl-main --query 4090 --json
loaf cloud catalog image --account autodl-main --query PyTorch --json
loaf cloud profile list --json
loaf cloud profile show train --json
loaf cloud acquire gpu-a --account autodl-main --profile train --dry-run --json
loaf cloud search --account autodl-main --profile train --json
```

With a known profile and the user's authorization for its scope, acquire directly:

```bash
loaf cloud acquire gpu-a --account autodl-main --profile train --wait 30m --yes --json
```

When running the experiment was also requested, use the new target without
changing the project default:

```bash
loaf run --target gpu-a --wait 10s --json -- python3 smoke.py
```

Humans use `loaf cloud acquire -i` for the focused GPU flow (compute, host/conditions, environment/storage, review). Other resource operations remain direct commands; bare cloud shows offline help. Agents use complete parameters and `--json`, never `-i` or simulated keystrokes; a PTY does not enable forms. Catalog queries find canonical model/region/image values, not stock. Prefer a named profile for repeated work, not a wrapper script. `cloud account use NAME` changes a personal default, not frozen bindings. Offline dry-run is optional and reads no credentials/network/state; its `pending_checks` still require live validation.

For an explicitly authorized continuous wait, use `--continuous --max-compute-hourly-price PRICE`; omit `--wait`. Continuous mode runs in the foreground until one success or cancellation. Paid expansion also needs `--max-storage-daily-price PRICE`. A bounded `--wait` remains at most 24h and explicit `0s` still means one attempt. To purchase only a chosen search candidate, use `--offer OFFER_ID --max-compute-hourly-price PRICE` without monitoring; it will not substitute another host. New continuous/fixed-offer paths verify image catalog metadata before approval. Developer-token Pro cannot provide this catalog/quoted mode; keep its existing explicit finite unquoted path distinct.

Finite stdout remains one envelope. Opt-in `--progress=json` provides versioned progress NDJSON on stderr, not another stdout stream; default `--json` is silent on stderr. Cloud `context` exposes scope/stage/request/instance, and `next_actions[].command` is an argv array. `error.issues` and structured help relationships locate missing/conflicting inputs. Suggestions still require the applicable user authority. Terminal EOF or a `created` progress event is not readiness. Follow original identities, not a new acquisition.

`search` is one read-only query; `watch --wait 10m --json` monitors read-only in the foreground with NDJSON. Inspect hard requirements, unknown fields, observation time and pagination coverage; “no match” in a bounded result is not proof of no platform stock. Profiles are optional; direct GPU/memory/region/image/price flags override project/user defaults. GPU memory is per card; an hourly cap covers whole-instance compute, not storage or total spend. The user's separate safety ceiling cannot be raised by project/CLI overrides.

After user authorization, `acquire --yes` supplies noninteractive confirmation for at most one paid instance, not training, resource release or automatic shutdown. Inventory and prices can race allocation. Session-backed Pro has catalog/quote search and monitoring; numeric `--gpu-spec-id` differs from developer `--gpu-spec` UUID. Unknown GPU memory/vendor stays ineligible when required; aggregate stock is not a spec reservation. The developer-token Pro adapter has no verified read-only stock/prequote capability: use an explicit spec/image/CUDA and `--allow-unquoted` only with specific user approval for unquoted allocation attempts. It cannot bypass a configured price cap or unverified hardware floors. Do not treat Elastic stock as Pro stock.

### Clone an existing environment and data

For an authorized same-region migration, use the native acquisition command rather than a temporary API wrapper:

```bash
loaf cloud acquire gpu-copy --account autodl-main --product standard \
  --clone-from old-gpu --max-compute-hourly-price 3.00 --wait 24h --yes --json
```

The source is a stopped ordinary instance ID or cloud alias. Loaf waits for stock only in its region and copies both disks; it never falls back to blank creation. Source image and default disk expansion are inherited. Omit image/spec/CUDA/system-disk flags; conflicting regions or smaller disks are errors. Clone waiting defaults to 30m unless configured; explicit zero remains one attempt. `ready` additionally requires successful data copying, not just SSH. Use `cloud info` for `clone_data_status`; on timeout or `CLONE_FAILED`, keep the receipt and resume/recover that same instance with `cloud bind --request`, not another acquisition.

Save `request_id` and the returned instance ID, including on failure. `CLOUD_UNCONFIRMED` (exit 3) means a resource may already exist or billing may have started. Inspect `cloud requests`, check the platform and `cloud reconcile <request-id> --instance <known-id>`; never rent a replacement automatically. `--no-instance --yes` clears duplicate protection only after explicit human confirmation that no resource was created, not an inference from an incomplete list.

`cloud bind --request <request-id>` recovers the saved alias and retries connection to the same created resource without renting. `ready` means platform running, authenticated SSH and saved target, not a ready ML environment. For an existing resource use `cloud bind gpu-a --instance <id> --account <name>`. Provider connections accept a new host key in the original call, including JSON; no separate [SSH trust step](recovery.md#first-ssh-trust) is needed. Passwords are fetched transiently for native SSH; default `cloud credentials` redacts them. Do not request `--show-password` merely to run an experiment.

`cloud start/stop <id-or-alias> --yes --wait 2m --json` affect the whole instance, potentially other users' work. They require separate authority from `loaf stop`, which stops one experiment. Acceptance is not completion and storage may keep billing after power-off. For an unconfirmed power operation reconcile the same saved request/instance rather than replaying it. Removing/rebinding aliases does not redirect previously submitted experiments.

Offline `profile save NAME [resource flags] [--replace]` stores effective personal preferences, not payment authority. `remove NAME --yes` removes only the personal copy; same-named project fields keep precedence. Preview `next_actions` can include a round-trippable effective acquisition argv without `--yes` or credentials. Preserve explicit zero, empty and false. A later-window pinned offer may use the generated `loaf-offer-v1:…` locator; do not edit it or treat it as reserved stock.

Acquire/bind readiness defaults to five minutes (maximum ten; explicit zero is one bounded check). Safe `failure_kind`/`failure_reason` distinguish trust, authentication, configuration and transport. Follow the first-use policy for a new key; a changed key requires investigation, never automatic cleanup. Keep `probe_attempts` distinct from allocation attempts. Do not automatically replay an unconfirmed foreground exec.

Standard continuous scans rotate bounded windows and wrap at the end. Partial windows at different times do not form a full snapshot. The selected candidate's original window and ID are rechecked before allocation. An interactive pre-allocation edit cancels the old wait and requires new consent; a created/uncertain receipt cannot start over. Use `cloud requests` to discover recovery records rather than guessing the last request ID.
