# Recover the same operation

[Home](../../README.md) · [Command reference](commands.md) · [简体中文](../zh-Hans/troubleshooting.md)

## Troubleshooting

```bash
loaf check --trace --json
loaf pull <run-id> --trace --json
```

`--trace` adds a bounded `trace` object to a finite JSON result, including stage timings, connection resolutions, successful private masters, reconnects and read retries. Retry events carry stable reasons such as `private_master_exit`; a recovered operation can succeed while its trace retains the initial failure. Human traces go to stderr. Collection is opt-in and omits commands, endpoints, request bodies, passwords, tokens and environment values.

Timings may be nested, so do not sum them. Private-master counters apply to cloud runner connections, not the user's static SSH sharing. The trace profiles the SSH workflow, including its cloud connection lookup, not unrelated cloud catalog/allocation HTTP operations. Details stop at 512 events with `truncated: true`, while counters remain complete. `check --trace` stays read-only remotely unless `--runner` is also explicit. Trace is not supported for following streams, foreground `exec`, interactive terminals, tunnels or cloud watch.

### Native Windows and WSL

On Windows, `masters_established` stays zero: independent SSH connections are intentional. Each cloud lookup still uses the frozen instance. Mutations are never replayed. A missing or mixed tool error requires MSYS2 `openssh` and `rsync` in one installation; set `LOAF_MSYS2_ROOT` to its root, such as `C:\msys64`, rather than `usr\bin`. Test SSH configuration and authentication with that same MSYS2 installation. Use forward slashes in SSH config paths and quote spaces; keep host-key verification enabled.

Downloads reporting `UNSAFE_PATH` may contain Windows reserved names, trailing spaces/dots, case collisions, reparse points or unsupported links. Use compatible output names or a Linux filesystem in WSL; Loaf never renames outputs or overwrites a differently cased local name. Keep state and credentials on local NTFS with private ACLs. Preserve rejected stores when correcting permissions; do not delete uncertainty evidence. WSL uses separate Linux state and credentials. Native Unix-socket Agent adapters return `DESTINATION_UNSUPPORTED`; use Webhooks or a verified adapter inside WSL, without substituting sessions or bypassing permissions.

Connection failures include a stable `error.stage`, such as `cloud_connection`, `ssh_connect`, `runner_verify`, `sync`, `launch`, `observe`, or `pull`, while retaining the original error code and available SSH/provider diagnostics. A runner verification failure is not itself evidence of a damaged executable.

Finite read-only probes, status/log queries, output checks, and downloads make at most one extra attempt after a recognized transient transport failure, with a short cancellable backoff. Authentication, host-key, configuration, and path failures are not retried. Default log streams are not restarted after partial output; explicitly opt in to cursor recovery with `logs -f --reconnect` or `--resume`. Source sync, runner upload, submission, stop, cleanup, and foreground commands are never automatically replayed. Downloads retain completed files and reuse the original run and destination.

For missing source files, use `loaf sync --dry-run` to inspect Git and Loaf exclusions. A project inside a parent repository's ignored directory remains subject to that ignore rule. For a failed command, inspect `loaf logs`; an accepted run can still fail because an interpreter, dependency, or input is missing. For a lost submission or stop confirmation, query the saved ID instead of starting a replacement task.
