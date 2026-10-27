# Foreground streams and acknowledged logs

```bash
loaf exec --json -- nvidia-smi
```

Use `exec` for an authorized foreground machine operation, not as a faster submission path for local project code. It streams bounded NDJSON output and returns the remote exit result, with no source sync, setup, saved run ID, or durable execution guarantee. Its default directory is the remote user's home; `--cwd` overrides it. Preserve argv after `--`; explicitly invoke a Shell for Shell syntax.

`EXEC_UNCONFIRMED` (exit 3) means execution may have happened. Do not automatically replay a mutating command after cancellation or connection loss. A confirmed nonzero remote exit is `EXEC_FAILED` (exit 1), not a transport failure. Choose `run --wait` when even a short experiment needs synchronized sources and a recoverable result.

`ssh` opens a native human terminal. `forward <remote-port> --local-port <local-port>` keeps a dedicated loopback SSH tunnel in the foreground; it does not start or check an application. Neither accepts JSON. Closing the forward removes that tunnel, not the remote service or another SSH master.

For explicitly requested continuous logs, `logs <run-id> -f --reconnect --json` allows one cursor-safe transient reconnection. Save `data.run.log.cursor` after consuming a chunk, or `data.log.cursor` from a finite query. Use `logs <run-id> -f --resume <cursor> --json` to continue manually; do not combine resume with tail. A changed/replaced log returns `LOG_CHANGED`; obtaining a fresh tail is an explicit reset, not automatic gap recovery. Default following remains single-connection. Cancellation or a failed output consumer never restarts a stream.

`logs -f --resume-file /tmp/cursor.json --recovery-budget 2m --json` loads an acknowledged cursor and permits consecutive transient failures within a cumulative disconnected-wait budget. JSON/pipes never auto-save processing acknowledgement: consume a chunk, then call `logs <run-id> --resume-file /tmp/cursor.json --ack-cursor <cursor> --json` offline. Human terminals checkpoint after successful writes. Keep checkpoint files outside source trees. Default opt-in recovery still retries once.
