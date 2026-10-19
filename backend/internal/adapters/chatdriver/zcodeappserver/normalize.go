package zcodeappserver

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/zcodeappserver/zcodeproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// normalizeNotification translates one provider notification into AO events
// per the table in DESIGN 4.2, as corrected by the Phase 1 captures (the
// testdata fixtures). The tracker supplies the turn correlation; the clock is
// passed in so the function stays deterministic under test.
//
// Malformed params never panic: every case decodes into its zcodeproto struct
// and returns nil on error. Anything the table does not list is dropped —
// inventing semantics for a frame AO does not understand is worse than silence.
func normalizeNotification(n notification, t *turnTracker, _ time.Time) []ports.ChatEvent {
	switch n.Method {
	case zcodeproto.MethodStateUpdated:
		return normalizeStateUpdated(n.Params, t)
	case zcodeproto.MethodComputerUseOperationEvent:
		return normalizeOperationEvent(n.Params, t)
	case zcodeproto.MethodSessionEvent:
		return normalizeSessionEvent(n.Params, t)
	case zcodeproto.MethodInteractionProviderRuntimeHeadersCancelled:
		// STATIC: the server gave up waiting for credentials AO did not supply.
		// Never observed on the API-key route (U0 closed); kept so a build that
		// sends it explains the failed turn that follows.
		return []ports.ChatEvent{{
			Kind:           ports.ChatEventError,
			ProviderTurnID: t.currentTurn(),
			Err:            ports.NewChatProviderFailure("Provider credentials request timed out", "", nil),
		}}
	}
	return nil
}

// normalizeStateUpdated handles the PROVEN progress signal. prompt_started is
// on the wire before the session/send acknowledgement, which is why the tracker
// is armed before the request is written (DESIGN 3.6). prompt_failed also
// follows a cancelled turn (p4-stop.ndjson); the fallback only fires when
// session/event did not settle the turn first.
func normalizeStateUpdated(params json.RawMessage, t *turnTracker) []ports.ChatEvent {
	var p zcodeproto.StateUpdatedParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil
	}
	switch p.Reason {
	case zcodeproto.ReasonPromptStarted:
		return one(t.observeStarted("", ""))
	case zcodeproto.ReasonPromptCompleted:
		return one(t.observeCompleted(domain.TurnStateCompleted, nil))
	case zcodeproto.ReasonPromptFailed:
		return one(t.observeCompleted(domain.TurnStateFailed,
			ports.NewChatProviderFailure("Turn failed", "", nil)))
	}
	return nil
}

// normalizeOperationEvent handles computer-use/operation-event, which arrives
// whether or not the session is subscribed and carries the native turn id.
func normalizeOperationEvent(params json.RawMessage, t *turnTracker) []ports.ChatEvent {
	var p zcodeproto.OperationEventParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil
	}
	switch p.Kind {
	case zcodeproto.OperationTurnStarted:
		return one(t.observeStarted(p.TurnID, ""))
	case zcodeproto.OperationTurnCompleted, zcodeproto.OperationTurnFailed:
		// Never a settlement. PROVEN order (p1, p4, p5, p1-turn-failed-provider):
		// this frame precedes session/event turn.completed / turn.failed by a
		// few milliseconds, and settling here would close the turn before the
		// authoritative event arrives — losing resultType "cancelled", the
		// committed response, the usage and the structured error. Every open
		// conversation is subscribed (a refused session/subscribe fails Start
		// and Resume), so session/event settles the turn and state.updated
		// prompt_completed/prompt_failed, always last on the wire, is the only
		// fallback.
		return nil
	case zcodeproto.OperationToolScheduled, zcodeproto.OperationToolStarted:
		if p.ToolCallID == "" {
			return nil
		}
		// On a subscribed session the session/event tool.updated frame with the
		// same eventId follows and owns the row; this is the fallback for an
		// unsubscribed one. Both are emitted on the same item id, so the
		// projector folds them.
		t.rememberTool(p.ToolCallID, p.ToolName, nil)
		call := t.tool(p.ToolCallID)
		return []ports.ChatEvent{{
			Kind:           ports.ChatEventActivityStarted,
			ProviderTurnID: turnFor(t, p.TurnID),
			ProviderItemID: p.ToolCallID,
			ActivityKind:   kindForTool(call.name),
			ActivityStatus: domain.ActivityStatusRunning,
			Summary:        toolSummary(call.name, call.input),
			Detail:         toolDetail(call.name, call.input, nil),
		}}
	case zcodeproto.OperationSessionClosed:
		return []ports.ChatEvent{closedThreadState()}
	}
	return nil
}

// normalizeSessionEvent handles session/event, the subscribed stream that
// carries assistant text, tool lifecycle and the authoritative settlement.
func normalizeSessionEvent(params json.RawMessage, t *turnTracker) []ports.ChatEvent {
	var p zcodeproto.SessionEventParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil
	}
	switch p.Type {
	case zcodeproto.EventTurnStarted:
		var payload zcodeproto.TurnStartedPayload
		if err := json.Unmarshal(p.Payload, &payload); err != nil && len(p.Payload) > 0 {
			return nil
		}
		// payload.inputId echoes the inputId sent on session/send, i.e. the
		// minted id the tracker is armed with (PROVEN, U3).
		return one(t.observeStarted(p.TurnID, payload.InputID))

	case zcodeproto.EventModelStreaming:
		var payload zcodeproto.ModelStreamingPayload
		if err := json.Unmarshal(p.Payload, &payload); err != nil {
			return nil
		}
		return normalizeModelStreaming(payload, turnFor(t, p.TurnID), t)

	case zcodeproto.EventPartDelta:
		var payload zcodeproto.PartDeltaPayload
		if err := json.Unmarshal(p.Payload, &payload); err != nil {
			return nil
		}
		if payload.Field != "text" {
			return nil
		}
		// STATIC and never seen: honoured only if model.streaming text_delta was
		// not seen this turn, so a build that emits both does not double the text.
		if !t.latchTextSource(zcodeproto.EventPartDelta) {
			return nil
		}
		turn := turnFor(t, p.TurnID)
		return []ports.ChatEvent{{
			Kind:           ports.ChatEventMessageDelta,
			ProviderTurnID: turn,
			ProviderItemID: t.replyItem(payload.MessageID, turn),
			Delta:          payload.Delta,
		}}

	case zcodeproto.EventTurnCompleted:
		var payload zcodeproto.TurnCompletedPayload
		if err := json.Unmarshal(p.Payload, &payload); err != nil {
			return nil
		}
		turn := turnFor(t, p.TurnID)
		var events []ports.ChatEvent
		if payload.Response != "" {
			// The committed text: turn.completed.response repeats the text the
			// deltas streamed for the LAST assistant message of the turn (PROVEN:
			// U2 for a single-message reply; p5-two-messages.ndjson for a reply
			// split around a tool call, where the earlier message is not
			// repeated), so it settles only the latest reply row. It is "" after
			// session/stop (p4-stop.ndjson), and then nothing is emitted: the
			// store's settle overwrites the row's text, so a message.completed
			// with "" would erase the streamed partial reply. Earlier message
			// rows and an interrupted turn's row are closed by the controller's
			// turn-level settle with their streamed text intact.
			events = append(events, ports.ChatEvent{
				Kind:           ports.ChatEventMessageCompleted,
				ProviderTurnID: turn,
				ProviderItemID: t.replyItem("", turn),
				Text:           payload.Response,
			})
		}
		if usage, ok := usageFrom(payload.Usage); ok {
			events = append(events, ports.ChatEvent{Kind: ports.ChatEventUsage, ProviderTurnID: turn, Usage: usage})
		}
		state, failure := turnStateFromResultType(payload.ResultType)
		return append(events, one(t.observeCompleted(state, failure))...)

	case zcodeproto.EventTurnFailed:
		var payload zcodeproto.TurnFailedPayload
		if err := json.Unmarshal(p.Payload, &payload); err != nil {
			return nil
		}
		// The provider's prose is surfaced with no auth interpretation: U12 is
		// moot on the API-key route, and the only auth-class failure observed
		// (Z.AI 1113, HTTP 429) is a quota condition the user resolves at the
		// provider, not a credential AO can re-collect.
		return one(t.observeCompleted(domain.TurnStateFailed, providerFailure(payload.Error)))

	case zcodeproto.EventToolUpdated:
		var payload zcodeproto.ToolUpdatedPayload
		if err := json.Unmarshal(p.Payload, &payload); err != nil {
			return nil
		}
		return normalizeToolUpdated(payload, turnFor(t, p.TurnID), t)

	case zcodeproto.EventPermissionResolved:
		var payload zcodeproto.PermissionResolvedPayload
		if err := json.Unmarshal(p.Payload, &payload); err != nil {
			return nil
		}
		if payload.RequestID == "" && payload.ToolCallID == "" {
			return nil
		}
		// RequestID may be empty: `Uhr` makes requestId optional and toolCallId
		// required (both were present on every captured frame). The conversation
		// pump fills it from its toolCallId index so the parked request is
		// cleared either way, and turns the event into input.resolved when the
		// parked request was a user-input one (p6-user-input.ndjson: a reply to
		// interaction/requestUserInput resolves as permission.resolved
		// decision:"modify").
		detail, _ := json.Marshal(map[string]any{
			"toolCallId": payload.ToolCallID, "toolName": payload.ToolName,
			"decision": payload.Decision, "reason": payload.Reason,
		})
		return []ports.ChatEvent{{
			Kind:           ports.ChatEventApprovalResolved,
			ProviderTurnID: turnFor(t, p.TurnID),
			ProviderItemID: payload.RequestID,
			RequestID:      payload.RequestID,
			Detail:         detail,
		}}

	case zcodeproto.EventUserInputResolved:
		// STATIC: never observed (see permission.resolved above).
		var payload zcodeproto.UserInputResolvedPayload
		if err := json.Unmarshal(p.Payload, &payload); err != nil || payload.RequestID == "" {
			return nil
		}
		return []ports.ChatEvent{{
			Kind:           ports.ChatEventInputResolved,
			ProviderTurnID: turnFor(t, p.TurnID),
			ProviderItemID: payload.RequestID,
			RequestID:      payload.RequestID,
		}}

	case zcodeproto.EventSessionTitleUpdated:
		var payload zcodeproto.TitleUpdatedPayload
		if err := json.Unmarshal(p.Payload, &payload); err != nil {
			return nil
		}
		return []ports.ChatEvent{{Kind: ports.ChatEventThreadRenamed, Title: payload.Title}}

	case zcodeproto.EventSessionClosed:
		return []ports.ChatEvent{closedThreadState()}
	}
	// session.updated, streamRecovery.updated, permission.requested (the
	// server request carries the same request), checkpoint/rewind/steer/part
	// events: dropped.
	return nil
}

// normalizeModelStreaming maps the kind-discriminated model.streaming payload
// (PROVEN kinds: text_delta, reasoning_delta, tool_input_start,
// tool_input_delta, tool_input_end, tool_call) onto the message, reasoning and
// tool rows.
func normalizeModelStreaming(payload zcodeproto.ModelStreamingPayload, turn string, t *turnTracker) []ports.ChatEvent {
	reasoning := reasoningItem(payload.AssistantMessageID, turn)
	switch payload.Kind {
	case zcodeproto.StreamingTextDelta:
		if !t.latchTextSource(zcodeproto.EventModelStreaming) {
			return nil
		}
		events := completeReasoning(t, reasoning, turn)
		return append(events, ports.ChatEvent{
			Kind:           ports.ChatEventMessageDelta,
			ProviderTurnID: turn,
			ProviderItemID: t.replyItem(payload.AssistantMessageID, turn),
			Delta:          payload.Delta,
		})

	case zcodeproto.StreamingReasoningStart:
		// STATIC: never observed; the first reasoning_delta opens the row.
		if !t.openReasoning(reasoning) {
			return nil
		}
		return []ports.ChatEvent{reasoningStarted(reasoning, turn)}

	case zcodeproto.StreamingReasoningDelta:
		// The row must exist before its first delta or the controller drops the
		// delta; there is no reasoning_start on the wire (p1, p5).
		var events []ports.ChatEvent
		if t.openReasoning(reasoning) {
			events = append(events, reasoningStarted(reasoning, turn))
		}
		return append(events, ports.ChatEvent{
			Kind:           ports.ChatEventReasoningDelta,
			ProviderTurnID: turn,
			ProviderItemID: reasoning,
			Delta:          payload.Delta,
		})

	case zcodeproto.StreamingToolInputStart:
		// The tool call's name is first announced here; tool.updated scheduled
		// repeats it, tool.updated result does not (p5-approval-allow.ndjson).
		t.rememberTool(payload.ToolCallID, payload.ToolName, nil)
		return completeReasoning(t, reasoning, turn)

	case zcodeproto.StreamingToolInputDelta, zcodeproto.StreamingToolInputEnd:
		// The streamed JSON text of the input; the parsed object arrives whole
		// on tool_call, which is what the row is built from.
		return nil

	case zcodeproto.StreamingToolCall:
		// The only frame that carries the input object: tool.updated scheduled
		// says inputOmitted:true, inputRef:"model_stream". Remembered here and
		// read back when the scheduled frame opens the activity row.
		t.rememberTool(payload.ToolCallID, payload.ToolName, payload.Input)
		return nil
	}
	return nil
}

// completeReasoning settles an open reasoning row once the message moves on
// to text or a tool call.
func completeReasoning(t *turnTracker, itemID, turn string) []ports.ChatEvent {
	if !t.closeReasoning(itemID) {
		return nil
	}
	return []ports.ChatEvent{{
		Kind:           ports.ChatEventActivityCompleted,
		ProviderTurnID: turn,
		ProviderItemID: itemID,
		ActivityKind:   domain.ActivityKindReasoning,
		ActivityStatus: domain.ActivityStatusCompleted,
		Summary:        "Reasoning",
	}}
}

func reasoningStarted(itemID, turn string) ports.ChatEvent {
	return ports.ChatEvent{
		Kind:           ports.ChatEventActivityStarted,
		ProviderTurnID: turn,
		ProviderItemID: itemID,
		ActivityKind:   domain.ActivityKindReasoning,
		ActivityStatus: domain.ActivityStatusRunning,
		Summary:        "Reasoning",
	}
}

// normalizeToolUpdated maps the kind-discriminated tool.updated payload onto
// activity lifecycle events on one item id (the toolCallId).
func normalizeToolUpdated(payload zcodeproto.ToolUpdatedPayload, turn string, t *turnTracker) []ports.ChatEvent {
	if payload.Kind == zcodeproto.ToolBatch {
		// A per-batch summary {toolCallIds, successCount, errorCount} with no
		// toolCallId of its own; every call in it already settled through its
		// own result frame (PROVEN).
		return nil
	}
	if payload.ToolCallID == "" {
		return nil
	}
	t.rememberTool(payload.ToolCallID, payload.ToolName, payload.Input)
	call := t.tool(payload.ToolCallID)
	switch payload.Kind {
	case zcodeproto.ToolScheduled, zcodeproto.ToolStarted:
		return []ports.ChatEvent{{
			Kind:           ports.ChatEventActivityStarted,
			ProviderTurnID: turn,
			ProviderItemID: payload.ToolCallID,
			ActivityKind:   kindForTool(call.name),
			ActivityStatus: domain.ActivityStatusRunning,
			Summary:        toolSummary(call.name, call.input),
			Detail:         toolDetail(call.name, call.input, nil),
		}}
	case zcodeproto.ToolProgress:
		// STATIC: never observed. Whether the tails are cumulative or incremental
		// is unknown, so only the extension beyond the previous tail is emitted
		// (DESIGN 4.2). Each stream is diffed against its own memory.
		stdout := payload.StdoutTail
		if stdout == "" {
			stdout = payload.OutputPreview
		}
		var events []ports.ChatEvent
		for _, stream := range []struct{ key, tail string }{
			{payload.ToolCallID + "\x00out", stdout},
			{payload.ToolCallID + "\x00err", payload.StderrTail},
		} {
			if delta := t.progressDelta(stream.key, stream.tail); delta != "" {
				events = append(events, ports.ChatEvent{
					Kind:           ports.ChatEventCommandOutputDelta,
					ProviderTurnID: turn,
					ProviderItemID: payload.ToolCallID,
					Delta:          delta,
				})
			}
		}
		return events
	case zcodeproto.ToolResult:
		// PROVEN: {result:{success, content, perf}, duration}; no toolName, so
		// the kind and summary come from what the earlier frames remembered.
		status := domain.ActivityStatusCompleted
		extra := map[string]any{}
		var result zcodeproto.ToolResultPayload
		if len(payload.Result) > 0 && json.Unmarshal(payload.Result, &result) == nil {
			if result.Success != nil && !*result.Success {
				status = domain.ActivityStatusFailed
			}
			if result.Content != "" {
				extra["output"] = result.Content
			}
			if code, ok := exitCodeFromPerf(result.Perf); ok {
				extra["exitCode"] = code
			}
		}
		if payload.Duration > 0 {
			extra["durationMs"] = payload.Duration
		}
		return []ports.ChatEvent{{
			Kind:           ports.ChatEventActivityCompleted,
			ProviderTurnID: turn,
			ProviderItemID: payload.ToolCallID,
			ActivityKind:   kindForTool(call.name),
			ActivityStatus: status,
			Summary:        toolSummary(call.name, call.input),
			Detail:         toolDetail(call.name, call.input, extra),
		}}
	case zcodeproto.ToolError:
		// STATIC: never observed.
		extra := map[string]any{}
		if len(payload.Error) > 0 {
			extra["error"] = payload.Error
		}
		return []ports.ChatEvent{{
			Kind:           ports.ChatEventActivityCompleted,
			ProviderTurnID: turn,
			ProviderItemID: payload.ToolCallID,
			ActivityKind:   kindForTool(call.name),
			ActivityStatus: domain.ActivityStatusFailed,
			Summary:        toolSummary(call.name, call.input),
			Detail:         toolDetail(call.name, call.input, extra),
		}}
	}
	return nil
}

// exitCodeFromPerf reads perf.detail.command.exitCode off a Bash result
// (PROVEN shape, p5-approval-allow.ndjson).
func exitCodeFromPerf(perf json.RawMessage) (int, bool) {
	if len(perf) == 0 {
		return 0, false
	}
	var p struct {
		Detail struct {
			Command *struct {
				ExitCode *int `json:"exitCode"`
			} `json:"command"`
		} `json:"detail"`
	}
	if err := json.Unmarshal(perf, &p); err != nil || p.Detail.Command == nil || p.Detail.Command.ExitCode == nil {
		return 0, false
	}
	return *p.Detail.Command.ExitCode, true
}

// providerFailure renders turn.failed.error as AO's opaque provider failure:
// the message is the title, and the detail carries the server's detail line,
// the error code and the attribution the provider route reported (PROVEN
// shapes: p1-turn-failed-provider.ndjson, p7-models-level-required.ndjson).
func providerFailure(e zcodeproto.ProtocolErrorDetail) error {
	var lines []string
	if detail := strings.TrimSpace(e.Detail); detail != "" {
		lines = append(lines, detail)
	}
	if e.Code != "" && !strings.Contains(e.Message, e.Code) {
		lines = append(lines, "code: "+e.Code)
	}
	if a := e.Attribution; a != nil && a.Source != "" {
		attribution := "attribution: source=" + a.Source
		if a.StatusCode != 0 {
			attribution += fmt.Sprintf(" status=%d", a.StatusCode)
		}
		if a.ProviderErrorCode != "" {
			attribution += " providerErrorCode=" + a.ProviderErrorCode
		}
		attribution += fmt.Sprintf(" retryable=%t", a.Retryable)
		if !strings.Contains(e.Detail, "status=") {
			lines = append(lines, attribution)
		}
	}
	message := e.Message
	if message == "" {
		message = e.UnderlyingErrorMessage
	}
	return ports.NewChatProviderFailure(message, strings.Join(lines, "\n"), nil)
}

// kindForTool maps a zcode tool name onto AO's activity kind (DESIGN 4.2).
func kindForTool(toolName string) domain.ActivityKind {
	switch {
	case toolName == "Write", toolName == "Edit", toolName == "MultiEdit", toolName == "NotebookEdit":
		return domain.ActivityKindFileChange
	case strings.HasPrefix(toolName, "mcp__"):
		return domain.ActivityKindMCPTool
	default:
		return domain.ActivityKindCommand
	}
}

// toolSummary is the one-line label for a tool activity.
func toolSummary(toolName string, input json.RawMessage) string {
	if command := inputCommand(input); command != "" {
		return "Run " + command
	}
	if toolName == "" {
		return "Tool call"
	}
	return toolName
}

// toolDetail builds the neutral detail payload using codex's key vocabulary
// (command, cwd, toolName, server, arguments, output, exitCode, error,
// durationMs) because the UI renders those keys.
func toolDetail(toolName string, input json.RawMessage, extra map[string]any) []byte {
	detail := map[string]any{}
	if toolName != "" {
		detail["toolName"] = toolName
	}
	if strings.HasPrefix(toolName, "mcp__") {
		parts := strings.SplitN(toolName, "__", 3)
		if len(parts) >= 2 {
			detail["server"] = parts[1]
		}
	}
	if command := inputCommand(input); command != "" {
		detail["command"] = command
	}
	if len(input) > 0 && json.Valid(input) {
		detail["arguments"] = input
	}
	for key, value := range extra {
		detail[key] = value
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		return nil
	}
	return encoded
}

// inputCommand reads a `command` string off a tool input object when present.
func inputCommand(input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var object struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(input, &object); err != nil {
		return ""
	}
	return object.Command
}

// turnStateFromResultType maps turn.completed.resultType onto AO's turn state.
// "success" and "cancelled" are PROVEN (p1, p4). Unknown (including empty) is
// failed rather than completed, like codex's turnStateFrom: claiming success
// AO did not observe is worse than reporting an honest unknown failure.
func turnStateFromResultType(resultType string) (domain.TurnState, error) {
	switch resultType {
	case "success":
		return domain.TurnStateCompleted, nil
	case "cancelled", "canceled", "interrupted":
		return domain.TurnStateInterrupted, nil
	case "":
		return domain.TurnStateFailed, ports.NewChatProviderFailure("Turn ended without a result type", "", nil)
	default:
		return domain.TurnStateFailed, ports.NewChatProviderFailure(resultType, "", nil)
	}
}

// usageFrom decodes turn.completed.usage into AO's accounting (PROVEN, U9).
// Context occupancy is never derived from totals (ports.ChatUsage rule); it
// comes from the post-turn session/read projection instead.
func usageFrom(raw json.RawMessage) (*ports.ChatUsage, bool) {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return nil, false
	}
	var usage zcodeproto.TurnUsage
	if err := json.Unmarshal(raw, &usage); err != nil {
		return nil, false
	}
	if usage.InputTokens == 0 && usage.OutputTokens == 0 && usage.TotalTokens == 0 {
		return nil, false
	}
	total := usage.TotalTokens
	if total == 0 {
		total = usage.InputTokens + usage.OutputTokens
	}
	return &ports.ChatUsage{
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
		CachedTokens: usage.CacheReadTokens,
		TotalTokens:  total,
		TotalsKnown:  true,
	}, true
}

func closedThreadState() ports.ChatEvent {
	return ports.ChatEvent{
		Kind:        ports.ChatEventThreadState,
		ThreadState: &ports.ChatThreadState{Status: domain.ThreadStatusClosed, Closed: true},
	}
}

// reasoningItem is the item id of a message's reasoning row. The streamed
// reasoning_delta carries assistantMessageId and no partId (PROVEN), so the
// row is keyed by the message; the history reader uses the same key.
func reasoningItem(messageID, turn string) string {
	if messageID != "" {
		return "zcode-reasoning:" + messageID
	}
	return "zcode-reasoning:" + turn
}

// turnFor resolves a native turn id to AO's id, falling back to the turn in
// flight: the open one, or the armed one whose start signal the pump has not
// processed yet. Server requests are answered on their own goroutines ahead of
// the pump, so an approval raised right after session/send would otherwise be
// filed under no turn (DESIGN 3.5).
func turnFor(t *turnTracker, native string) string {
	if native != "" {
		if minted := t.mintedFor(native); minted != "" {
			return minted
		}
	}
	return t.active()
}

func one(ev *ports.ChatEvent) []ports.ChatEvent {
	if ev == nil {
		return nil
	}
	return []ports.ChatEvent{*ev}
}

// rawID stringifies a JSON-RPC id, which may be a number or a string. `null`,
// absent and empty all yield "".
func rawID(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return ""
	}
	return strings.Trim(s, `"`)
}
