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

## The v0.5 state boundary

v0.5 starts fresh schema-1 SQLite stores and does not import older JSON indexes
or cloud receipts. Re-select a project machine using `loaf machine use NAME`;
a new project identity gets a fresh managed directory under the default remote
base. An explicit `remote_dir` must be suitable for the new format, not an
existing incompatible project.

Use the matching older CLI for old tasks and unresolved cloud requests. An empty
new database does not prove an older paid request did not happen; reconcile it
before acquiring again. Existing schema-1 data needs no migration for this
command-layout change. Unknown formats fail without overwrite.
