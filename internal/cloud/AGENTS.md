# Cloud acquisition contracts

These rules also apply to Resources workflows, AutoDL adapters and credential
changes. See the public cloud guide and provider tests for exact request formats.

- Flags and `cloud acquire -i` build the same typed application input. The wizard
  owns forms and unsubmitted draft state; cloud/application owns matching,
  validation and budget interpretation. Do not add policy to pages or build a form
  framework. One stage driver owns transitions; approval keeps independent values.
- Match every hard condition; unknown hardware cannot satisfy a minimum. GPU memory
  is per card, compute price covers the requested instance, and expanded storage has
  its own daily cap. Use exact decimal CNY and the adapter's real quote contract.
- Account, auth mode, compute product and frozen instance identity are distinct.
  Current defaults never reinterpret an old receipt or binding. Credentials stay in
  their explicit keyring/file store, never TOML or output; do not silently fall back
  between stores, products or session/developer APIs.
- Persist intent before paid/lifecycle mutation. An ambiguous response retains the
  same request/instance and blocks another purchase; only a confirmed no-stock
  rejection permits another allocation within the approved plan. Requote before
  creation. A read retry must not replay a mutation.
- Preview is local and read-only: no credentials, provider calls, state writes or
  allocated IDs. Real acquisition authorizes at most one instance and requires
  explicit payment consent. It never starts training or automatically releases or
  shuts down an instance.
- Fixed offers never substitute another card. Clone acquisition keeps the original
  principal/region/image/disk constraints and source instance; no fallback to ordinary
  creation. Changed conditions require approval again; unchanged approval is reused.
- A readiness failure ends with the original request/instance and a CLI continuation,
  not another rental or management menu. `--yes`/JSON/batch calls do not open forms.
- Keep provider HTTP errors classified and private: bounded responses, normal TLS,
  no redirects to arbitrary token destinations, and no raw secret response bodies.

Use existing simulated provider and PTY tests for changed behavior. Real stock,
account and billing checks need separate explicit authorization; fixture results
do not establish them. Keep new provider/adapter work driven by actual demand.
