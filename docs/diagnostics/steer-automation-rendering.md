# Steering message versus Automation block

## Finding

The screenshot is rendering the intended `SteerMessage` component, not an accidental human-message fallback. The visible bubble and the `Steered into the running turn` footer are produced by `frontend/src/renderer/components/chat/ChatTimelineItems.tsx:2167-2212`.

The exact bypass is structural: `ao send --steer` does not create a conversation message and does not persist `origin = automation`. It creates a completed `system` activity whose detail contains `event: "steer"`. The renderer checks that discriminator before the generic activity fallback and renders a right-aligned user-style steer bubble.

This is intentional in the current design. The backend comment at `backend/internal/service/chat/steer.go:632-639` says a steer must be an activity because appending a message would create a second turn and send the correction twice. The same comment says the `event` discriminator lets the client show the user's own words rather than a system notice. The frontend repeats that product decision at `frontend/src/renderer/types/conversation.ts:467-479` and `frontend/src/renderer/components/chat/ChatWorkspace.tsx:3664-3669`.

If the product decision is that cross-session steering is automation-originated and must look like an Automation block, the smallest safe fix is renderer-only: add an explicit sender/automation presentation mode to the steer activity payload or a dedicated event, then render that mode through a distinct component. Do not infer it from the `[from ...]` text prefix. Changing the persisted activity kind to a message or changing `origin` to automation would violate the current turn and at-most-once semantics.

## End-to-end trace

1. **CLI prefix and request.** `backend/internal/cli/send.go:79-93` reads `AO_SESSION_ID` and prepends the literal text `[from <session-id>] ` for all non-recovery sends. `:101-107` posts `Text`, `ClientMessageID`, and `RecoverOnly` to `POST sessions/{id}/conversation/steer-or-send`.
2. **HTTP intake.** `backend/internal/httpd/controllers/conversation_steer.go:34-76` decodes the request and constructs `ports.ChatUserMessage` with `Origin: domain.MessageOriginHuman` at `:55-58`. The normal `conversation/messages` route does the same at `backend/internal/httpd/controllers/conversations.go:752-777`. The CLI is therefore not structurally marked as automation at this boundary.
3. **Atomic steer-or-send.** `backend/internal/service/chat/steer.go:137-159` validates the handle and delegates to `Controller.SteerOrSend`. For an active turn, `:380-405` reserves the durable delivery handle, calls the provider steerer, and `:440-450` persists the accepted activity through `CompleteSteerDelivery`.
4. **Durable activity shape.** `backend/internal/service/chat/steer.go:640-685` calls `makeSteerActivity`, writing `Kind: ActivityKindSystem`, `Status: completed`, `Summary`, and JSON detail `{event: "steer", text, origin: "human", clientMessageId?, content?}`. The no-handle path uses `recordSteer` and `UpsertActivity` at `:640-654`; the idempotent path uses the same activity shape inside `CompleteSteerDelivery` at `:440-447`.
5. **Conversation API.** `backend/internal/httpd/controllers/conversations.go:1128-1147` maps messages with their durable `Origin`, while `:1158-1172` maps activities with `ActivityKind`, `Summary`, and typed `Detail`. `ConversationSnapshotResponse` has `sessionId` but no session display name at `backend/internal/httpd/controllers/dto.go:2758-2815`.
6. **Renderer classification.** `frontend/src/renderer/components/chat/ChatWorkspace.tsx:3605-3650` sends assistant messages to `AssistantMessage`, human-origin user messages to `HumanMessage`, and all other user messages to `OriginMessage`. Activities then pass through approval/input/compaction checks, and `isSteer(item)` at `:3664-3669` catches `system + detail.event === "steer"` and calls `SteerMessage`.

## Why worker reports look different

Worker and automation relays are ordinary user-role messages with `origin = automation`. `backend/internal/service/chat/service.go:2148-2169` builds relay sends with `Origin: domain.MessageOriginAutomation`; the service comment at `:2120-2128` explicitly says this attribution is durable. Worker-report piggybacking is also skipped for already-automation messages at `backend/internal/service/chat/service.go:1106-1113`, preserving the distinction.

Those messages reach the same snapshot `Messages` array, and the renderer sends non-human origins to `OriginMessage` at `ChatWorkspace.tsx:3616-3650`. `OriginMessage` renders the bordered, left-accented Automation-style block at `ChatTimelineItems.tsx:649-697`, using `senderLabel ?? origin`, a compact preview, and optional full Markdown expansion.

The screenshot therefore is not a failed `origin` classification. The steer never enters that message-origin branch. It is a deliberately separate activity path.

## Tests and interaction semantics

- Backend steering coverage: `backend/internal/service/chat/steer_test.go:190-240` verifies the accepted steer reaches the running provider turn and lands on the timeline. The helper decodes only `ActivityKindSystem` activities whose detail event is `steer`.
- HTTP coverage: `backend/internal/httpd/controllers/conversation_steer_test.go` covers steer and steer-or-send request and response contracts.
- Renderer coverage: `frontend/src/renderer/components/chat/ChatProviderSignal.test.tsx:280-306` verifies the steer text and the `Steered into the running turn` marker.
- Automation block coverage: `frontend/src/renderer/components/chat/ChatWorkspace.test.tsx:2180-2265` verifies browser feedback, long-report disclosure, short automation alerts, and safe session-link handling.
- Markdown and link behavior: `frontend/src/renderer/components/chat/ChatMarkdown.tsx:325-342` uses `SessionLinkedText` to linkify canonical `ao://sessions/...` URLs without parsing surrounding text as Markdown. `AppLink` at `frontend/src/renderer/components/AppLink.tsx:20-85` preserves keyboard-accessible anchors, context-menu copy, and in-app handlers. `ChatMarkdown.test.tsx:330-360` and `session-links.test.ts` cover these contracts.
- Accessibility: `SteerMessage` uses a real text span and an `aria-hidden` icon (`ChatTimelineItems.tsx:2206-2209`), while `OriginMessage` uses a real button with `aria-expanded` for long reports (`:681-693`). The existing steer test checks the visible marker but does not assert an accessible name or role for the enclosing message. Any redesign should preserve the real text, keyboard focusability for links, and a non-color-only distinction.
- Markdown handling: the current short steer body is plain text with `whitespace-pre-wrap`, like a user bubble. It deliberately does not pass steer text through `ChatMarkdown`, so Markdown syntax remains literal and canonical session URLs are not currently linkified inside a steer. This differs from expanded Automation reports, which use `ChatMarkdown`, and is a product-level consistency choice worth deciding if steer bodies should carry links.

## Sender display name and safe clickable session link

### Current source of the prefix

The prefix is generated only in `backend/internal/cli/send.go:79-80` from the raw `AO_SESSION_ID`. It is persisted as ordinary message text in the steer detail by `makeSteerActivity`; there is no separate sender-session-id field in the steer detail. The existing test `backend/internal/cli/send_test.go:434-457` locks the current behavior as `[from aa-47] ...`.

### Is a display name available to the conversation renderer?

The durable session model does have `DisplayName` at `backend/internal/domain/session.go:164-188`, and workspace session data carries a user-facing `title` in `frontend/src/renderer/types/workspace.ts:70-90`. The conversation snapshot itself exposes only `sessionId` and conversation data, not the source session's display name (`backend/internal/httpd/controllers/dto.go:2758-2815`). `ConversationMessageResponse` also has no sender session identity or display label (`dto.go:2697-2710`). Therefore the renderer cannot safely resolve `[from agent-orchestrator-329]` to a name from this payload alone.

### Smallest safe design

Do not parse or rewrite the stored prefix in the renderer. The smallest robust contract is to add structured fields to the steer detail, for example `senderSessionId` and an optional `senderDisplayName`, populated by the CLI/daemon from the source session record. Keep the immutable id as the link target and treat the name as display-only. The renderer can then display `[from <display name>]` and wrap only the label in an `AppLink` whose `href` is a canonical `ao://sessions/<project-id>/<sender-session-id>` URL. `SessionLinkedText` and `ChatMarkdown` already provide the safe link plumbing, but a dedicated inline label component is preferable to feeding the whole steer body through Markdown.

The project id is required by the current canonical link grammar. `frontend/src/renderer/lib/session-links.ts:8-60` accepts exactly `ao://sessions/<project>/<session>`, and `use-session-link-navigation.ts` resolves it against the workspace cache, rejects malformed, missing, inaccessible, or terminated sessions, and emits a user-facing toast. `AppLink` keeps the result keyboard accessible and supports copy/open-in-AO context actions.

Fallback behavior should be explicit:

- If the source session exists and has a non-empty display name, show that name and link it by immutable session id.
- If it was renamed after the steer, keep the send-time display-name snapshot in the historical steer row. The link remains id-based, never name-based. This preserves what the sender was called when the instruction was issued and avoids rewriting historical conversation content.
- If the source session is missing, inaccessible, or the source metadata is unavailable, show the raw session id as plain text or a non-link fallback such as `agent-orchestrator-329`. Never fabricate a link or turn untrusted text into an `href`.
- If the current daemon cannot provide the new structured fields, preserve the stored text exactly for backward compatibility.

The implementation intentionally uses the send-time snapshot for `senderDisplayName`; it does not resolve names again while reading a conversation. A future product decision can add live-name resolution, but that would make historical rows change when sessions are renamed and would require an explicit cache and consistency policy.

This requires a product decision about whether a cross-session steer is visually a human instruction or an automation event. If it stays human-style, add only the structured sender label and link. If it becomes Automation-style, add an explicit presentation discriminator and tests for both modes. In either case, keep the current activity persistence, idempotency, turn attachment, and provider-echo suppression unchanged.

## Recommended tests for any implementation

1. Backend: `makeSteerActivity` persists `event`, sender session id, display label, and immutable link identity independently of the text body; legacy rows without the fields still decode.
2. CLI: a source session with a renamed display name uses structured metadata rather than a text prefix; missing lookup falls back to the raw id; existing `AO_SESSION_ID` behavior remains compatible during rollout.
3. API: conversation snapshots expose the new detail fields without leaking unrelated session metadata.
4. Renderer: steer remains distinct from `OriginMessage`; label text is visible and keyboard reachable; `ao://` href uses encoded project and session ids; missing and terminated sessions do not produce a clickable dead link.
5. Markdown/link safety: steer body text cannot inject an anchor through brackets or raw HTML; only the structured label is linked. Preserve `AppLink` context-menu copy and `useSessionLinkNavigation` failure toasts.
