# ACP integration with the shared AO Chat UI

Use this reference when the released native agent exposes ACP. Start with the
[upstream contract](integration-path.md#1-map-the-upstream-contract); an advertised
ACP flag or successful initialize response is not qualification. Use temporary
registration in an isolated candidate build for registered AO Chat verification;
enable production registration only after the required native and AO cases pass.

## Implementation path and source entry points

Reuse the existing path: native agent plugin -> provider ACP binding -> shared ACP
transport/persistent host -> daemon Chat service and API -> shared renderer.
The frontend consumes AO conversation state; it does not speak provider ACP.
All source links below refer to this checkout. Inspect nearby implementations and
current tests before editing; these are routing locations, not a mandatory list
of files to modify for every agent.

| Layer | Inspect | Implement or verify |
| --- | --- | --- |
| Native installation/auth/profile | `backend/internal/adapters/agent/<harness>/` | Reuse the existing plugin for binary resolution, auth and isolated runtime environment. Do not bundle a second CLI or silently select a different executable. |
| Provider binding | `backend/internal/adapters/chatdriver/<harness>acp/`; [nativeacp/driver.go](../../../../backend/internal/adapters/chatdriver/nativeacp/driver.go) | Add a small wrapper and tests using `nativeacp.Config`: native arguments/env, version compatibility, permission/config mappings, additive instructions and truthful capabilities. Qualify the pinned executable, not just wrapper construction. |
| Protocol and host | [acp/driver.go](../../../../backend/internal/adapters/chatdriver/acp/driver.go); [persistenthost/host.go](../../../../backend/internal/adapters/chatdriver/persistenthost/host.go) | Reuse ACP transport and detached-host lifecycle. Extend shared code only for a demonstrated protocol gap, with regressions for other bindings. Preserve identity, permissions, replay and cancellation across reconnect. |
| Contracts and admission | [ports/chat.go](../../../../backend/internal/ports/chat.go); [ports/chat_controller.go](../../../../backend/internal/ports/chat_controller.go) | Follow typed driver/conversation events, errors and permission-specific production capability checks. A default capability in `nativeacp` is not native proof; explicitly disable unsupported optional features and keep required gaps unregistered. |
| Chat registry | [chatdriver/registry/registry.go](../../../../backend/internal/adapters/chatdriver/registry/registry.go) | Add the proven constructor and registry tests. `SupportsChat` means a driver is registered; runtime Probe/handshake determines current availability. Never silently convert an explicit Chat request into a TUI session. |
| Service/controller | [service/chat/service.go](../../../../backend/internal/service/chat/service.go); [service/chat/controller.go](../../../../backend/internal/service/chat/controller.go) | Reuse controller ownership, queue, persistence and replay. Keep lifecycle/status/permission decisions in the daemon. Avoid provider-specific branches in generic services unless the existing contracts cannot represent proven behavior. |
| HTTP/settings contract | [controllers/conversations.go](../../../../backend/internal/httpd/controllers/conversations.go); [controllers/settings.go](../../../../backend/internal/httpd/controllers/settings.go) | Reuse conversation snapshot, messages, decision, interrupt and configuration routes. Settings expose `chatHarnesses`; registration availability is separate from verified login/readiness. Change DTO/spec sources and regenerate with `npm run api` only if the wire contract changes. |
| Session creation/mount | [TaskComposer.tsx](../../../../frontend/src/renderer/components/TaskComposer.tsx); [SessionView.tsx](../../../../frontend/src/renderer/components/SessionView.tsx) | Verify Chat selection uses daemon settings and the resulting persisted mode is `chat`. Existing task creation may explicitly offer TUI fallback; prove Chat itself rather than counting that separate choice as a Chat pass. |
| Live event bridge | [httpd/events.go](../../../../backend/internal/httpd/events.go); [lib/event-transport.ts](../../../../frontend/src/renderer/lib/event-transport.ts) | Reuse `GET /api/v1/events` and the shared renderer event transport. Verify CDC conversation events invalidate/refetch the query/snapshot path; `useConversation` is not the event-subscription owner. Preserve ordering, reconnect and owning-host routing. |
| Live data/commands | [useConversation.ts](../../../../frontend/src/renderer/hooks/useConversation.ts); [SessionChatSurface.tsx](../../../../frontend/src/renderer/components/chat/SessionChatSurface.tsx) | Reuse the generated typed API, owning-host routing, snapshots, daemon event updates and command wiring. Preserve idempotency, sequence/revision ordering and history pagination. Do not create a renderer-side ACP client or infer state from terminal text. |
| Shared Chat presentation | [ChatWorkspace.tsx](../../../../frontend/src/renderer/components/chat/ChatWorkspace.tsx); [ChatComposer.tsx](../../../../frontend/src/renderer/components/chat/ChatComposer.tsx); [TurnSettingsBar.tsx](../../../../frontend/src/renderer/components/chat/TurnSettingsBar.tsx) | Reuse timeline/tool/plan rendering, composer, decisions and settings. Add a new shared presentation behavior only when a proven capability cannot be represented; add focused component tests. A new agent normally needs no separate Chat screen. |
| Per-feature gating | [types/conversation.ts](../../../../frontend/src/renderer/types/conversation.ts) | Follow `can(snapshot, capability)` and existing callback/refusal gates. Missing capabilities mean unknown/unsupported, not all enabled. Keep unsupported controls unavailable and preserve backend typed refusal as a backstop. |

## When frontend changes are necessary

First register and qualify the backend binding against the current API. If the
shared UI already represents its events/configuration, only the existing agent
identity/avatar and capability wiring may need updates. Do not copy the Chat
screen per provider or move daemon semantics into React.

When a proven feature is missing, extend the relevant shared component and its
existing tests: message/tool/plan presentation in `ChatWorkspace`, input/decision
behavior in `ChatComposer`, or model/permission/config options in `TurnSettingsBar`
and `SessionChatSurface`. Trace the actual component owning the behavior before
editing. Preserve native option IDs, offered decisions, errors and disabled states;
never invent supported models or permission modes to make a picker work.

Qualify attachments, images, steering, compaction, MCP and structured input only
when claimed; ACP declarations alone are insufficient. Production admission
requires streaming, interrupt and resume, plus approvals for non-bypass
permissions. Explicitly authorized bypass excludes only the approval requirement;
it is a separate scope, not a substitute for approve/reject qualification.
Chat support does not imply TUI-to-Chat handoff or shared native history with TUI.

## Chat verification checklist

Use an isolated registered AO Chat session on the authorized host. Prepare the
owned daemon/UI using the checkout's desktop-lab instructions; record native
version/model, AO/UI/build identity, session/native-ID digest, permissions and
source scope. Apply the [Doctor instance/capture checks](audit-journeys.md) using
this Chat launch record (base URL, runfile, PID/port, binary and data directory),
not a required TUI-runner report. Confirm root `/readyz` PID/build ownership and
fresh applicable native/Chat admission evidence. Cached availability is not login
proof; explicit diagnostic scope retains any unverified auth label.

Verify `GET /sessions/{sessionId}` and
`GET /desktop/sessions/{sessionId}/workspace` identify the owned Chat session and
disposable worktree. For Chat input readiness, read
`GET /sessions/{sessionId}/conversation`: require
the intended live controller, actual provider initialization/load, and a ready
controller with no running/queued turns or pending decisions before an ordinary
send. Approval and cancellation cases deliberately exercise their own pending/
running states. Chat has no TUI composer/generation prerequisite. Observe its
shared UI input state, snapshot and native protocol instead.
Routes below are relative to `/api/v1/`; use the selected session's IDs and the
current generated request schema. Exercise the actual UI action as well as its
API/native effects. The TUI audit runner cannot execute or qualify these cases.

| Journey | Entry/action | Readiness or precondition | Required observable proof |
| --- | --- | --- | --- |
| New Chat task | Use **New task** with a registered Chat-capable agent; inspect `POST /sessions` and selected session. | Chosen version/config/profile and supported permission mode; fresh runtime availability. | Persisted `mode: chat`; real native `session/new` with stable identity; initial task once; edits in the AO worktree; source checkout unchanged. A TUI fallback is not this case. |
| Follow-up and stream | Type an ordinary unique file task in **Message the agent** and use **Send message**; correlate `POST /sessions/{sessionId}/conversation/messages` and snapshot updates. | Correct Chat session/controller; no unresolved decision or unrelated draft. | Actual partial output before completion, then final text/tool/plan output where supported; expected file mutation once; controller ready with no running/queued turns. An optimistic local echo alone is not delivery. |
| Load/history | Reopen the same session and inspect `GET /sessions/{sessionId}/conversation`; separately exercise native `session/load` through restore/restart. | Persisted native identity and supported history for the same workspace. | Timeline content/order retained without duplicates, paged history correct, native load reaches the exact conversation, and a continuation recalls prior context and preserves the existing proof. Reopening a cached UI alone does not prove native load. |
| Approve | In a supported non-bypass mode, request a distinct controlled operation and choose an offered approval decision in the shared UI. Correlate `POST /sessions/{sessionId}/conversation/approvals/{requestId}/resolve`. | A positively observed current pending request; record its exact request ID and offered options. | No protected side effect before approval; selected native decision reaches that request once; only the authorized operation executes and the UI clears the resolved decision. |
| Reject | Request a separate controlled operation and choose the offered rejection. | New current pending request; do not reuse a resolved/stale ID. | Native rejection received, denied operation has no side effect, and the session remains usable. A model refusal without a new permission request is a different control. |
| Cancel active tool | With an owned command-start witness while the turn is running, use **Stop turn**; correlate `POST /sessions/{sessionId}/conversation/interrupt`. | PID plus kernel-start identity observed; no competing approval or user draft. The stop control depends on actual composer state; never clear unrelated input to expose it. | Exact executing child/descendants stop within the measured bound, no scheduled late artifact appears, controller/queue settles, and an ordinary continuation works in the same native conversation. UI settlement or provider socket closure alone is insufficient. |
| UI reconnect | Disconnect/reconnect the UI's daemon event subscription or reload the owned UI, then continue. | Keep the same daemon/controller/native identity; capture before/after snapshots. | Shared UI catches up without lost/duplicated messages, approvals or turns; draft ownership remains intact; a new user turn executes once. This does not prove daemon replacement. |
| Daemon/host recovery | Replace only the owned daemon using the supported detached-host recovery path, then reconnect UI. | Record host/native child PIDs and kernel starts plus saved session/native identity first. | Correct host adoption/reconnection, history and current state preserved, no duplicate dispatch, functional continuation; no fresh-conversation fallback. Record retained versus replaced processes. |
| Kill/restore and new instructions | Use **Archive session** plus confirmation, then **Restore session** where offered. After confirmed Kill, add/read back fresh private instructions before restoring. | Saved native ID/workspace/history; termination and exact-process cleanup verified independently. | Exact native load, same AO workspace/history, usable Chat controller, ordinary continuation edits the old proof and consumes new private instructions. Use independent ordinary-task controls, not a prompt asking for the token. Keep an unrelated live control usable. |
| Negative restore/capability cases | In exclusive scratch data, exercise missing history/workspace mismatch and unsupported optional settings or inputs. | A control that reaches the relevant native validation; preserve/restore any owned withheld history. | Typed failure with no fresh session or silent permission downgrade; unsupported UI control unavailable and daemon refusal truthful. Missing binary/login is not proof of missing-history rejection. |

Record every non-pass case and retain original failures. Use synthetic providers
and fixtures for protocol/component regressions, plus released native and actual
AO UI proof for integration claims. Keep standalone native proof, API proof and
UI proof separate. Follow the [evidence contract](evidence-and-handoff.md) for
screenshots, source provenance, diagnostic labels, cleanup and durable attachment.
A screenshot establishes a visible state; it does not replace process/history or
instruction assertions. Cleanup and evidence survival follow the journey map.
