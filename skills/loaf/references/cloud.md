# Authorized cloud operations

Use existing SSH targets without cloud configuration. When the user authorizes cloud operations, choose the configured account and product explicitly. The adapters support a verified main/sub session for ordinary AutoDL and Pro, plus a separate developer-token Pro adapter; unsupported combinations do not switch control planes. An account's default product is only a preference. `--product` overrides profile `product`, then that default, but cannot redirect an already bound alias. A human can use `cloud login <name>` with hidden input, choosing `--credential-store keyring` or explicit unencrypted `file`; new-login defaults belong in user `[cloud] credential_store`, not project TOML. Login remembers a replayable password digest unless disabled. Treat it as a password; never request it in chat. Use configured references, not browser-token extraction, credential files, printed tokens or training environments. `cloud login <name>` explicitly reauthenticates with a saved digest; do not automatically repeat password failures or bypass verification. Logout deletes only the selected credential entry.

```bash
loaf cloud search --account autodl-main --profile train --json
loaf cloud acquire gpu-a --account autodl-main --profile train --wait 30m --yes --json
loaf machine use gpu-a --json
loaf run --wait 10s --json -- python3 smoke.py
```

`search` is one read-only query; `watch --wait 10m --json` monitors read-only in the foreground with NDJSON. Inspect hard requirements, unknown fields, observation time and pagination coverage; “no match” in a bounded result is not proof of no platform stock. Profiles are optional; direct GPU/memory/region/image/price flags override project/user defaults. GPU memory is per card; an hourly cap covers whole-instance compute, not storage or total spend. The user's separate safety ceiling cannot be raised by project/CLI overrides.

`acquire --yes` authorizes at most one paid instance, not training, resource release or automatic shutdown. Inventory and prices can race allocation. Session-backed Pro has catalog/quote search and monitoring; numeric `--gpu-spec-id` differs from developer `--gpu-spec` UUID. Unknown GPU memory/vendor stays ineligible when required; aggregate stock is not a spec reservation. The developer-token Pro adapter has no verified read-only stock/prequote capability: use an explicit spec/image/CUDA and `--allow-unquoted` only with specific user approval for unquoted allocation attempts. It cannot bypass a configured price cap or unverified hardware floors. Do not treat Elastic stock as Pro stock.

### Clone an existing environment and data

For an authorized same-region migration, use the native acquisition command rather than a temporary API wrapper:

```bash
loaf cloud acquire gpu-copy --account autodl-main --product standard \
  --clone-from old-gpu --max-compute-hourly-price 3.00 --wait 24h --yes --json
```

The source is a stopped ordinary instance ID or cloud alias. Loaf waits for stock only in its region and copies both disks; it never falls back to blank creation. Source image and default disk expansion are inherited. Omit image/spec/CUDA/system-disk flags; conflicting regions or smaller disks are errors. Clone waiting defaults to 30m unless configured; explicit zero remains one attempt. `ready` additionally requires successful data copying, not just SSH. Use `cloud info` for `clone_data_status`; on timeout or `CLONE_FAILED`, keep the receipt and resume/recover that same instance with `cloud bind --request`, not another acquisition.

Save `request_id` and the returned instance ID, including on failure. `CLOUD_UNCONFIRMED` (exit 3) means a resource may already exist or billing may have started. Inspect `cloud requests`, check the platform and `cloud reconcile <request-id> --instance <known-id>`; never rent a replacement automatically. `--no-instance --yes` clears duplicate protection only after explicit human confirmation that no resource was created, not an inference from an incomplete list.

`cloud bind gpu-a --request <request-id>` retries connection to the same created resource without renting. `ready` means platform running, authenticated SSH and saved target, not a ready ML environment. For an existing resource use `cloud bind gpu-a --instance <id> --account <name>`. Noninteractive first-host trust failures need a human/native SSH decision; do not bypass verification. Passwords are fetched transiently for native SSH; default `cloud credentials` redacts them. Do not request `--show-password` merely to run an experiment.

`cloud start/stop <id-or-alias> --yes --wait 2m --json` affect the whole instance, potentially other users' work. They require separate authority from `loaf stop`, which stops one experiment. Acceptance is not completion and storage may keep billing after power-off. For an unconfirmed power operation reconcile the same saved request/instance rather than replaying it. Removing/rebinding aliases does not redirect previously submitted experiments.
