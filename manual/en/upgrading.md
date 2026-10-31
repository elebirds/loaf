# Upgrade without losing access to a task

[Home](../../README.md) · [Installation](installation.md) · [简体中文](../zh-Hans/upgrading.md)

## Update with the installer

Re-run the [installation command](installation.md#one-command-install-or-update)
to replace the installed package with the newest public release, including RCs.
The installer keeps no older binaries and does not modify configuration,
credentials, task databases or remote processes. This does not migrate
unsupported development histories; the formal compatibility boundary below
still applies.

## Compatibility starts with the first formal 1.0 release

The compatibility baseline is **`v1.0.0`**, not the development releases leading
up to it. A newer formal `1.x` CLI supports the existing operations on work
created by earlier formal `1.x` releases: Runs, queues, cloud receipts,
subscriptions, received events and delivery records. Original Run IDs, software
versions, machine bindings and input/output choices remain unchanged.

For example, after updating from `v1.0.0` to a newer formal `1.x` CLI, use the same
project and IDs:

```bash
loaf status RUN_ID --json
loaf logs RUN_ID
loaf pull RUN_ID
```

Stopping, terminal cleanup and preparation abandonment keep their existing
authorization and eligibility requirements. Older formal Coordinators can keep
dispatching their original queue while a compatible newer CLI queries it or
submits tasks using the shared operation set. A new feature unavailable in an
older component needs that feature's explicit upgrade; it does not disable the
component's existing operations.

Software identity is separate from supported JSON, wire, event and database
formats. `inspect ID` shows both the Run's original `version` and the current
`client_version`; `compatible` is not an exact string-equality check. Deployment
still verifies the exact packaged helper at its versioned path. A new control
helper does not replace or restart an active Supervisor, replay a launch, or
rewrite the saved Run binding.

This promise is for newer formal `1.x` clients reading and managing earlier
formal `1.x` work. Older clients operating on newer formats, downgrades and
cross-major upgrades are outside this policy. Unknown formats fail without
overwriting their data. Third-party session availability, credentials and
retention of remote disks remain separate prerequisites.

## Formal and development state stay separate

Formal `1.x` builds use the fixed local path `stable-v1/state.db` under the
platform's Loaf state directory. The directory names the supported release
series, not the SQLite schema; a normal `1.x` software update does not create a
fresh empty history. Run `version --json` to identify the installed build.

Development builds and prereleases, including `v1.0.0-rc.*`, use
`state-v4/state.db`. They do not import historical stores and require matching
development software identities for saved work. Local and project databases use
schema 4; the machine queue uses schema 2. Databases record their formal or
development series separately, so copying a development database into the
formal directory does not convert it into supported formal state.

Remote projects retain `.loaf-control-v2/state.sqlite3`; that namespace does not
change with the software version or let another helper bypass a retained
no-start claim. A formal helper rejects development project databases. Use a
new managed remote project root for the first formal release, keeping the local
repository and original remote work intact.

The machine/user queue likewise keeps its one ledger at
`~/.local/share/loaf/queue-v1/state.sqlite3`. A formal build refuses a development
ledger rather than starting a second capacity ledger beside it. Finish existing
development queue work with its original tools and retire its Coordinator before
explicitly archiving that old ledger, or choose a remote identity without one.
Never remove a ledger still used by a running Coordinator.

## Move from development to formal releases once

`v0.x`, development and RC histories are unsupported: no automatic import,
database conversion, legacy adapter or rebinding of their work is provided.
Before replacing the only development CLI, keep it under a versioned filename
with the directories and receipts needed to manage its outstanding work. Use it
to finish, observe, stop or retrieve that work and reconcile uncertain paid
operations. A fresh formal history is not evidence that an earlier allocation
did not happen.

Existing configuration that already satisfies the current TOML format can still
be read; no historical configuration converter is added, and credentials are not
deleted. Register formal notification destinations in the intended session.
Do not move development records into the formal store or replace active
Supervisors to make them appear compatible.

For an explicitly installed Bridge service, stop its old process, update its
executable path and restart it against the same formal state directory. Saved
cursors, deduplication and delivery evidence are retained; a possible send with
lost confirmation remains unconfirmed rather than being resent. No general
remote component updater or background migration service is installed.

## Command names changed during development

| Previous call | Current call |
| --- | --- |
| `loaf add NAME --ssh HOST` | `loaf machine add NAME --ssh HOST` |
| `loaf use NAME` | `loaf machine use NAME` |
| `loaf targets` | `loaf machine` or `loaf machine list` |
| `loaf targets NAME` | `loaf machine show NAME` |
| `loaf remove NAME` | `loaf machine remove NAME` |
| `loaf ps [ID]` | `loaf status [ID]` |

Previous entry points return `BAD_CONFIG`, reason `command_replaced`, operation
exit 2 and the replacement usage. They do not perform the old operation.
Update scripts and agent instructions; no execution alias silently forwards them.
`loaf --version` and `loaf version` share the existing build result.

This command cleanup does not rename `[ps].limit`, change existing JSON data
keys, convert databases, repoint saved bindings, or alter output locations.
Machine list/show still return `targets` and `project` in JSON.

`VERSION_MISMATCH` retains `expected_version` and `actual_version` for unsupported
software pairs. Normal runner operations deploy and verify the current packaged
control helper when needed; read-only previews never deploy it. This does not
import an unsupported historical database or authorize replacing a Supervisor.
