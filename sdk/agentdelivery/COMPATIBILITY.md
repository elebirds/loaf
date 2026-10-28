# Compatibility evidence

Checked on macOS on 2026-10-28. Evidence covers these exact native versions and
modes; other desktop versions and organization admission need separate checks.

| Destination | Tested version and mode | Observed evidence |
| --- | --- | --- |
| Codex | CLI `0.164.0-alpha.11.1`, public Unix WebSocket, a newly created ephemeral App Server thread | SDK returned `accepted` with a turn ID. An independent observer saw that same turn complete with the exact confirmation text and no command, file-change or MCP-tool items. The SDK still reports acceptance only. |
| Current Codex desktop chat | Desktop `26.1021.31345` build `21828`, bundled CLI `0.164.0-alpha.11.1`, experimental private peer IPC | The packaged Loaf CLI verified and bound the actual pre-existing active chat. Its Bridge delivered one unique fact; the same chat received the matching native `agentdelivery.external_event` item. Receipt event/message/turn identities correlate, with one attempt and no substitute session or server. The SDK reports `accepted`, not an automatic processing ACK. |
| Claude Code | `2.1.311`, interactive custom development Channel, explicit native warning confirmation | The helper initialized with MCP `2025-11-25`, SDK returned `written`, and the target terminal displayed the uniquely identified channel event. Native model requests then failed with upstream HTTP 502; no `ack_event` call occurred. `Lookup` correctly retained `written` and the message was not resent. |
| Claude ordinary custom Channel admission | Public research-preview policy | A bare custom server needs platform admission; development-mode evidence does not establish ordinary allowlist support. Organization policy remains enforced. |
| Webhook | Isolated Go HTTP fixture | HTTP acceptance, redirect refusal and ambiguous-response handling; no Agent-processing claim. |

The earlier App Server and Claude checks each sent one confirmation event to a
new isolated session. The App Server thread was `readOnly` with
`on-request` approvals; its dedicated server and workspace were removed. Claude
had no built-in tools and retained manual approvals. Its interrupted test
session and project trust/history were removed with the native project-scoped
`claude purge` command. The checks copied no credentials, changed no global
integration settings, ran no experiments and acquired no resources.

The later current-desktop check was separately authorized for the original
conversation. Binding and owner discovery were read-only. A synthetic,
explicitly marked acceptance fact used isolated Loaf state and the real packaged
Bridge; the receiver observed its native tool-output item in that same chat.
The existing owner inherited its workspace/sandbox/approvals. No desktop process
was restarted and no thread was created, resumed, steered or substituted. The
saved `here` destination then verified the same identity in ordinary user state.

Desktop peer IPC is an installed-application protocol, not the documented public
App Server API. Compatibility relies on versioned owner-discovery/follower
contracts and explicit untrusted-input support. Other desktop builds require
compatible contracts; failures never fall back to another thread or server.

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
