# Compatibility evidence

Checked on macOS on 2026-10-28. Version pins and exact modes matter; this is
not a promise about other desktop versions or organization settings.

| Destination | Tested version and mode | Observed evidence |
| --- | --- | --- |
| Codex | CLI `0.164.0-alpha.11.1`, public Unix WebSocket, a newly created ephemeral App Server thread | SDK returned `accepted` with a turn ID. An independent observer saw that same turn complete with the exact confirmation text and no command, file-change or MCP-tool items. The SDK still reports acceptance only. |
| Claude Code | `2.1.311`, interactive custom development Channel, explicit native warning confirmation | The helper initialized with MCP `2025-11-25`, SDK returned `written`, and the target terminal displayed the uniquely identified channel event. Native model requests then failed with upstream HTTP 502; no `ack_event` call occurred. `Lookup` correctly retained `written` and the message was not resent. |
| Claude ordinary custom Channel admission | Public research-preview policy | A bare custom server needs platform admission; development-mode evidence does not establish ordinary allowlist support. Organization policy remains enforced. |
| Webhook | Isolated Go HTTP fixture | HTTP acceptance, redirect refusal and ambiguous-response handling; no Agent-processing claim. |

Each real-platform check sent one confirmation event to a new isolated session,
never an existing user conversation. The Codex thread was `readOnly` with
`on-request` approvals; its dedicated server and workspace were removed. Claude
had no built-in tools and retained manual approvals. Its interrupted test
session and project trust/history were removed with the native project-scoped
`claude purge` command. No credentials were copied, global integration settings
were changed, experiments were run, or resources were acquired.

Automated tests cover exact-session/workspace rejection, wrong identities,
private Unix WebSocket framing, lost confirmation without replay, MCP protocol
negotiation, transport-write versus explicit ACK evidence, duplicate/conflicting
message IDs, and shutdown of owned connections. The module is also tested with
`GOWORK=off`, independently of Loaf.

The Codex transport uses WebSocket frames over the Unix socket. Sending raw
JSONL through the native byte proxy does not perform the required HTTP upgrade;
the adapter therefore connects using the pinned WebSocket library directly.

Protocol references:
[Codex App Server](https://learn.chatgpt.com/docs/app-server),
[Claude Channels reference](https://code.claude.com/docs/en/channels-reference).
