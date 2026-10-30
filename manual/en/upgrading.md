# Upgrade without losing access to a task

[Home](../../README.md) · [Installation](installation.md) · [简体中文](../zh-Hans/upgrading.md)

## New command names

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

## Keep the binary that owns existing work

Software identity must match a saved task, independently of JSON/wire/database
format numbers. Keep the previous CLI under a versioned filename before replacing
the only copy needed by active tasks. Use it to observe, stop, or retrieve those
tasks. `inspect ID` can show the required version offline; a new CLI does not
migrate records, replace active supervisors, or download an older binary.

`VERSION_MISMATCH` includes `expected_version` and `actual_version`. For a saved
task, use its matching CLI. A current helper missing from an otherwise current
binding is a different issue: normal runner operations deploy and verify the
bundled helper automatically. Read-only previews never deploy it.

## The v0.6 state boundary

This development build starts fresh local schema-3 state in `state-v3`.
It does not import historical locators, subscriptions, receipts or destinations;
bind `here` again from the intended Codex chat. Prior stores and credentials are
not deleted. New remote projects use schema 3 in `.loaf-control-v2`, including
durable no-start receipts for abandoned preparation. Existing schema-2 projects
are rejected without conversion; use a new remote project directory for this
development build, preserving the original directory and its matching CLI.
Never hot-upgrade an active Supervisor. Historical formats and interfaces are
not supported by this development build. A fresh local store does not establish
that an older uncertain cloud allocation did not happen. Reconcile real paid
operations before any new explicitly authorized acquisition.
