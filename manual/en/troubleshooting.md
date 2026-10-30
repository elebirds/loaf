# Recover the same operation

[Home](../../README.md) · [Command reference](commands.md) · [简体中文](../zh-Hans/troubleshooting.md)

## Correct local input before connecting

```bash
loaf help machine add --json
loaf help run --json
```

Use `error.argument` and `error.reason` to locate the problem. If `error.issues`
is present, correct the listed local issues together. `error.help_command` is an
argv array for the relevant help page; it does not repeat the failed mutation.
Help and version work with unrelated configuration errors, so no account login,
credential lookup or connection check is needed just to learn the syntax.

Without `-i`, calls cannot fill missing values through a form, even with a PTY.
Supply them in the command or supported configuration, or explicitly use `-i`
for a human guide. A valid empty/zero/false
override is different from omission: `--wait 0s` means no wait, whereas
`status --limit 0` is out of range. Input errors omit argument values and config
excerpts; do not paste private input into a diagnostic report to compensate.

## Diagnose transport without replaying an operation

```bash
loaf check --trace --json
loaf pull <run-id> --trace --json
```

`--trace` adds a bounded `trace` object to a finite JSON result, including stage timings, connection resolutions, successful private masters, reconnects and read retries. Retry events carry stable reasons such as `private_master_exit`; a recovered operation can succeed while its trace retains the initial failure. Human traces go to stderr. Collection is opt-in and omits commands, endpoints, request bodies, passwords, tokens and environment values.

Timings may be nested, so do not sum them. Private-master counters apply to cloud runner connections, not the user's static SSH sharing. The trace profiles the SSH workflow, including its cloud connection lookup, not unrelated cloud catalog/allocation HTTP operations. Details stop at 512 events with `truncated: true`, while counters remain complete. `check --trace` stays read-only remotely unless `--runner` is also explicit. Trace is not supported for following streams, foreground `exec`, interactive terminals, tunnels or cloud watch.

Connection failures include a stable `error.stage`, such as `cloud_connection`, `ssh_connect`, `runner_verify`, `sync`, `launch`, `observe`, or `pull`, while retaining the original error code and available SSH/provider diagnostics. A runner verification failure is not itself evidence of a damaged executable.

Choose the next step from the available connection evidence:

- Unknown host key: verify the fingerprint with the machine administrator, then
  make the trust decision through native `loaf ssh --target lab`.
- Changed host key: investigate the change first; do not delete `known_hosts`
  entries or accept the replacement automatically.
- Authentication or configuration problem: inspect `loaf machine show lab` and
  the relevant SSH user, key/agent or config. Do not publish raw SSH diagnostics.
- Timeout or disconnect: a fresh `check` can gather new evidence. An
  `EXEC_UNCONFIRMED` result still requires checking what ran remotely before
  repeating the foreground command.

Use the actual saved alias in place of `lab`. To share diagnostics, export the
allowlisted `check --report` artifact; ordinary `check --json` is not a redacted
report and can include tool paths.

Finite read-only probes, status/log queries, output checks, and downloads make at most one extra attempt after a recognized transient transport failure, with a short cancellable backoff. Authentication, host-key, configuration, and path failures are not retried. Default log streams are not restarted after partial output; explicitly opt in to cursor recovery with `logs -f --reconnect` or `--resume`. Source sync, runner upload, submission, stop, cleanup, and foreground commands are never automatically replayed. Downloads retain completed files and reuse the original run and destination.

For missing source files, use `loaf sync --dry-run` to inspect Git and Loaf exclusions. A project inside a parent repository's ignored directory remains subject to that ignore rule. For a failed command, inspect `loaf logs`; an accepted run can still fail because an interpreter, dependency, or input is missing. For a lost submission or stop confirmation, query the saved ID instead of starting a replacement task.
