# Consume Loaf results

[Home](../../README.md) · [Commands](commands.md) · [简体中文](../zh-Hans/json.md)

## Read the envelope before the task result

Finite commands with `--json` write one JSON object to stdout:

```json
{"schema_version":1,"ok":true,"data":{}}
```

`data` is command-specific, not a universal empty object. On failure `error`
contains a stable `code` and localized `message`, plus an optional localized
`hint`. A failure can retain useful `data`; process exit alone is not a parser.
Fields not applicable to that result are omitted. `data`/`error` are omitted when
absent, not filled with dummy values. Consumers must tolerate additional fields.

| Operation exit | Meaning |
| --- | --- |
| 0 | CLI operation succeeded, even if the observed experiment failed |
| 1 | Other operation failure, including partial queries/downloads |
| 2 | Invalid parameters or configuration |
| 3 | Submission, stop, foreground execution, cleanup, cloud mutation, queue mutation or Agent delivery unconfirmed |

A task result has `run_id`, `version`, `target`, `remote_dir`, `status`,
`created_at`, and nullable `exit_code`. Optional fields include `submission`,
`phase`, `completed_at`, `signal`, `reason`, labels, log, query/save errors,
observation time, and cleanup metadata. Software `version` is separate from the
JSON `schema_version` and remote record format.

- `status`: `starting`, `running`, `succeeded`, `failed`, `stopped`, or `unknown`.
- `submission`: `preparing`, `uncertain`, or `submitted` when available. Confirmed
  acceptance survives a later observation failure.
- `exit_code: null`: no confirmed process exit result. Zero is a real exit code.
- `query_error`: this run could not be observed. It is not a training failure.
- `state_error`: local persistence/checkpoint failed. Verified status and exit
  results still count; do not replace them with `unknown`.
- `last_observation`: a dated saved public fact, not live status.
  `last_query_failure` separately records a past failed query.

Timestamps are RFC3339 with an explicit timezone; compare instants, not displayed
clock text. `wait_millis`, `stop_grace_millis` and `elapsed_millis` are milliseconds;
log offsets and file sizes are bytes. Resource prices are decimal strings in
CNY, compute per whole instance/hour, storage separately per day. GPU memory is
per card. CLI memory floors use GiB; platform disk-expansion flags use GB.

## Locate data for each command

| Command | Finite `data` shape |
| --- | --- |
| `run`, `status ID`, `stop`, `logs` | Task result; finite logs use `data.log` |
| `status`, `status --local` | `runs`, `total`, `eligible`, `examined`, `matched`, `unresolved`, `state_errors`, `limit`, `truncated`, `filters`; offline results add `source: "local"` |
| `inspect ID`, `status ID --local` | Task fields plus `source`, versions, compatibility, project/instance identity and frozen outputs |
| `run --dry-run` | Normalized argv, source plan, output/pull policy, provenance and environment names only |
| `machine`, `machine list/show` | `targets` array and `project` choice; JSON shape is unchanged by the new command names |
| `machine add/use/remove` | Saved alias, selection or removal result; no implied remote execution |
| `sync` | Effective policy and source/transfer result; dry-run selects local files only |
| `outputs` | Frozen roots, entries, missing roots and bounded inventory coverage |
| `pull` | `run_id`, `target`, `destination`, selected/downloaded/missing paths, policy and optional `changed_files` |
| Batch `pull` | `runs[].result` plus optional `runs[].error`; selected/completed/failed/unexamined counts |
| `check` | One target report; batch uses `checks[]` and selected/completed/failed/unexamined counts |
| `wait` | Every selected run and completed/pending/query_errors/state_errors/examined/unexamined/interrupted/timed_out coverage |
| `clean` | `runs[]` with per-run cleanup results and codes; execution outcome remains separate |
| `events list` | Source-bound `events`, `source_id`, `next_cursor` and `has_more`; events retain original source identity |
| `notify destination list`, `notify subscriptions`, `notify deliveries` | Safe destination, subscription or delivery rows; delivery dispatch and SDK evidence remain separate |
| `notify serve --once` | Received/attempted/accepted/unconfirmed/pending counts |
| `queue submit` | Queue `request`, optional Run observation or offline `preview`; admission does not imply execution acceptance |
| Other queue queries/control | Request(s), shared capacity `status`, or confirmed `cleaned` IDs |
| `queue clean` | Explicit `dry_run`, `request_ids` and queue `result` |
| `cloud search` | Frozen plan, matches, rejections, observation time and pagination coverage |
| `cloud acquire/bind` | Stage-dependent plan/receipt/binding, retaining known request and instance IDs on failure |
| `cloud list/info/requests` | Bounded instance page, one instance, receipt list or explicit receipt respectively |
| `cloud start/stop/reconcile` | Saved resource identity/receipt; acceptance is not power completion |
| `cloud account/login/logout/credentials` | Account/login result or connection info; credentials redact passwords unless explicitly revealed |
| `version`, `--version` | Software/build/record-format identity, platform, Go version, revision and nullable modified state |

Lists preserve partial success. In history, `limit` bounds eligible entries
examined, not the number of matches; unresolved rows remain visible. Do not
interpret a bounded empty list as proof that no matching tasks/resources exist.
For `OUTPUT_MISSING`, inspect downloaded and missing paths before deciding what
remains to retrieve. `changed_files` is absent when transfer completion is unverified.

## Structured help and input errors

```bash
loaf help run --json
```

`data.help` remains human-readable text. Additional fields are:

- `command`: full canonical command path.
- `arguments`, `flags`: `name`, `type`, localized `description`, `required`,
  `repeatable`, optional `variadic`/`list_format`, optional static `default`,
  `configurable`, and optional `config_key`.
- `commands`: child names and localized descriptions.
- `examples`: argv arrays, including the `loaf` executable name.

List format is `repeat` or `csv` when applicable. A static default is not the
current effective project/user value. Actual submission provenance is available
from `run --dry-run`. The historical `[ps].limit` configuration key is retained
although the command is now `status`.

Input diagnostics add optional `error.reason`, `error.argument`, and
`error.help_command` (argv). Configuration errors may add `error.location` with
`file`, `field`, and known one-based `line`/`column`; unavailable coordinates are
omitted. Reasons identify unknown command/option, missing argument, invalid type
or value, out-of-range, conflicts, invalid configuration, and replaced commands.
No raw argv dump, source excerpt or secret value is included.
`expected_version`/`actual_version` accompany task version mismatches.

## Consume streams one line at a time

| Command | NDJSON data |
| --- | --- |
| `logs ID -f --json` | `type: "chunk"` / `"end"`, task result in `run`; log text/cursor under `run.log` |
| `exec --json -- PROGRAM` | Chunks carry `type`, `stream` (`stdout`/`stderr`), `offset`, `encoding`, `data`; final `end`/`error` carries target and execution result |
| `cloud watch --wait 10m --json` | Change/heartbeat observations and a final `end` or error |
| `notify serve --json` without `--once` | `type: "delivery"` with a `delivery` row; final end/error includes `result` |

Each line has the same versioned envelope. An early stream error can be the only
line; never assume a chunk arrives first. Errors carry `error` and a
`data.type: "error"` marker. EOF without a final event does not prove success.
`ssh` and `forward` reject JSON and keep native terminal/tunnel behavior.
`notify channel` exclusively serves native MCP stdio, not Loaf envelopes or
NDJSON. It rejects `--json`/`--trace` with exit 2 and diagnostics on stderr,
leaving stdout empty. Structured `help notify channel --json` remains available.

Log text is bounded to 256 KiB per chunk; `truncated` means preceding bytes were
omitted. `offset` counts bytes in the source log, not Unicode characters. A
finite cursor is at `data.log.cursor`; stream cursors are at
`data.run.log.cursor`. A terminal checkpoints only after successful writes.
JSON/pipe consumers explicitly acknowledge processed cursors using offline
`logs ID --resume-file FILE --ack-cursor CURSOR --json`.

`exec` chunks are bounded to 32 KiB and maintain independent stdout/stderr byte
offsets. Decode `base64` when indicated; arbitrary output bytes are not guaranteed
to be UTF-8. A confirmed nonzero command exit is `EXEC_FAILED`; SSH 255 or
cancellation can be `EXEC_UNCONFIRMED` and must not trigger automatic replay.

## Complete example results

These fictional IDs, paths and build versions illustrate complete public task
results. The files are decoded by contract tests:

- [Successful experiment](../examples/succeeded.json): operation exit 0.
- [Failed experiment, successful query](../examples/training-failed.json): operation exit 0, training exit 7.
- [Accepted task, observation failure](../examples/accepted-unreachable.json): operation exit 1; keep the ID.
- [Partial history](../examples/partial-history.json): operation exit 1; keep both rows.
- [Verified result, local save failure](../examples/state-error.json): operation exit 2; keep the verified outcome.
