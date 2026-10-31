# Compatibility evidence

Checked on macOS on 2026-10-28 and 2026-10-31. Evidence covers these exact native
versions and modes; other desktop versions and organization admission need
separate checks.

| Destination | Tested version and mode | Observed evidence |
| --- | --- | --- |
| Codex | CLI `0.164.0-alpha.11.1`, public Unix WebSocket, a newly created ephemeral App Server thread | SDK returned `accepted` with a turn ID. An independent observer saw that same turn complete with the exact confirmation text and no command, file-change or MCP-tool items. The SDK still reports acceptance only. |
| Codex reference workflow | CLI `0.166.0-alpha.5.1`, public Unix WebSocket, one dedicated ephemeral original thread retained throughout two real CPU Runs | The original Agent submitted both Runs, received each terminal fact, queried and pulled each exact Run, read `result=42`, and answered. The idle fact began a follow-up turn; the busy fact entered the existing active turn during an intact 20-second command. Both receipts remained `accepted`, one attempt each; later collection sent nothing. |
| Current Codex desktop chat | Desktop `26.1021.31345` build `21828`, bundled CLI `0.164.0-alpha.11.1`, experimental private peer IPC | The packaged Loaf CLI verified and bound the actual pre-existing active chat. Its Bridge delivered one unique fact; the same chat received the matching native `agentdelivery.external_event` item. Receipt event/message/turn identities correlate, with one attempt and no substitute session or server. The SDK reports `accepted`, not an automatic processing ACK. |
| Current desktop result workflow | ChatGPT desktop host `26.1027.30824` build `22943`, bundled CLI `0.166.0-alpha.5.1`, experimental private peer IPC | A real CPU Run's completion entered the existing active chat. The receiving Agent then queried and pulled that exact Run and read `result=42`. The Bridge recorded one accepted attempt; recollection attempted zero sends. |
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
conversation. Owner discovery was read-only; binding saved the verified address.
A synthetic, explicitly marked acceptance fact used isolated Loaf state and the real packaged
Bridge; the receiver observed its native tool-output item in that same chat.
The existing owner inherited its workspace/sandbox/approvals. No desktop process
was restarted and no thread was created, resumed, steered or substituted. The
saved `here` destination then verified the same identity in ordinary user state.

## Complete Codex reference workflow

The 2026-10-31 check used an unpublished Loaf `v1.0.0` candidate, a dedicated
local Docker SSH server and two independent CPU Run working copies. The native
thread existed before either submission and retained its original project and
both Run IDs. Its settings were `workspaceWrite` with network access and the
owned fixture root, `on-request` approvals, and `gpt-6.1-sol` at low effort.
Delivery supplied no workspace, model or permission overrides.

The first completion reached the idle original thread. That Agent ran `status`
and `pull`, read the first downloaded file, and reported its actual result.
For the busy case, a separately authorized control turn performed
`/bin/sleep 20` in the same thread. The second completion arrived about one second into
that command, remained in that active turn, and did not interrupt it. Afterward
the Agent queried, pulled and read the second Run's output and answered with
its exact Run ID. The completed command duration and event timestamps establish
the busy behavior, rather than acceptance alone.

An earlier one-round collection during the first busy window occurred before
the source's next poll time and attempted zero sends. The extra bounded control
turn delivered the retained second fact; neither Run nor the first message was
recreated. Exactly two completion items and three completed native turns were
observed. Both delivery records have one attempt and `accepted` evidence; a
later collection had zero attempted sends, pending or unconfirmed items.

The owned server and SSH container were stopped after facts and downloaded
outputs were saved. No real cloud account, existing user session or global
integration settings were changed by this check.

## Current desktop result workflow

The later 2026-10-31 check used the packaged Loaf `v1.0.0-rc.1` candidate,
isolated local configuration/state and a dedicated Docker SSH target. The user
authorized testing delivery to this pre-existing desktop chat. `notify bind
NAME --current` verified its exact tool-environment thread and native owner.
The first probe was blocked by the tool sandbox; approved access to the local
desktop IPC allowed the same binding command to succeed.

An actual CPU Run finished with a downloaded `outputs/result.txt` containing
`result=42`. Its terminal fact entered the existing active chat as a native
`agentdelivery.external_event` tool output. After receiving it, the Agent used
the original test project and Run ID to execute `status` and `pull`, then read
that file. The receipt retained `accepted` with the original turn identity and
one attempt; another bounded collection attempted zero sends. The selected
desktop session kept its workspace and permissions throughout.

## Transport and lifecycle evidence

Desktop peer IPC is an installed-application protocol, not the documented public
App Server API. Compatibility relies on versioned owner-discovery/follower
contracts and explicit untrusted-input support. Other desktop builds require
compatible contracts; failures never fall back to another thread or server.

Automated tests cover exact-session/workspace rejection, wrong identities,
private Unix WebSocket framing, lost confirmation without replay, MCP protocol
negotiation, transport-write versus explicit ACK evidence, duplicate/conflicting
message IDs, and shutdown of owned connections. The module is also tested with
`GOWORK=off`, independently of Loaf.

Lifecycle regressions also cover a closed or changed Desktop owner, lost Claude
ACK caches after helper restart, and a Bridge killed after a possible send.
These are isolated peer/subprocess tests, not new native-version acceptance.

The Codex transport uses WebSocket frames over the Unix socket. Sending raw
JSONL through the native byte proxy does not perform the required HTTP upgrade;
the adapter therefore connects using the pinned WebSocket library directly.

Protocol references:
[Codex App Server](https://learn.chatgpt.com/docs/app-server),
[Claude Channels reference](https://code.claude.com/docs/en/channels-reference).

## Security and dependency checks

The newer development implementation adds consistent private Unix target/parent
ownership checks, explicit Webhook HTTPS/Bearer policy, 429 no-replay handling and
processed-first Claude cache eviction. Isolated tests cover these behaviors.
The pinned MCP Go SDK v1.6.1 LICENSE describes an Apache-2.0/MIT licensing
transition and CC-BY-4.0 documentation. The package collector includes the full
upstream LICENSE text rather than replacing it with an abbreviated label.
