package zcodeappserver

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/zcodeappserver/zcodeproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func notif(method, params string) notification {
	return notification{Method: method, Params: json.RawMessage(params)}
}

func sessionEvent(eventType, turnID, payload string) notification {
	return notif(zcodeproto.MethodSessionEvent,
		`{"type":"`+eventType+`","turnId":"`+turnID+`","sessionId":"sess-1","payload":`+payload+`}`)
}

func normalize(t *turnTracker, n notification) []ports.ChatEvent {
	return normalizeNotification(n, t, time.Unix(0, 0))
}

// armedTracker is a tracker with a prepared turn and the start observed, as it
// stands once session/send was accepted and prompt_started pumped.
func armedTracker(t *testing.T) *turnTracker {
	t.Helper()
	tracker := newTurnTracker()
	tracker.prepare("minted-1")
	if ev := tracker.observeStarted("t-native", ""); ev == nil || ev.ProviderTurnID != "minted-1" {
		t.Fatalf("observeStarted = %+v", ev)
	}
	return tracker
}

// normalizeFixture runs every notification of a fixture through a tracker
// armed with one minted turn and returns the events in order.
func normalizeFixture(t *testing.T, name string) []ports.ChatEvent {
	t.Helper()
	tracker := newTurnTracker()
	tracker.prepare("minted-1")
	var events []ports.ChatEvent
	for _, n := range fixtureNotifications(t, name) {
		events = append(events, normalize(tracker, n)...)
	}
	return events
}

func kinds(events []ports.ChatEvent) []ports.ChatEventKind {
	out := make([]ports.ChatEventKind, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.Kind)
	}
	return out
}

func TestEveryFixtureNotificationNormalizesWithoutPanic(t *testing.T) {
	for _, name := range fixtureNames(t) {
		t.Run(name, func(t *testing.T) {
			tracker := newTurnTracker()
			tracker.prepare("minted-1")
			for _, n := range fixtureNotifications(t, name) {
				func() {
					defer func() {
						if r := recover(); r != nil {
							t.Fatalf("%s panicked: %v", n.Method, r)
						}
					}()
					for _, ev := range normalize(tracker, n) {
						if ev.ProviderTurnID != "" && ev.ProviderTurnID != "minted-1" {
							t.Fatalf("%s emitted turn %q, want minted-1", n.Method, ev.ProviderTurnID)
						}
					}
				}()
			}
		})
	}
}

// The proven turn (p1-create-send-complete.ndjson): one turn.started on
// prompt_started, a reasoning row opened by its first delta and closed by the
// text, the streamed text, the committed response, the usage, one settlement
// from session/event, and nothing from the operation-event/prompt_completed
// fallbacks that follow.
func TestP1FixtureProducesTheProvenTurnLifecycle(t *testing.T) {
	events := normalizeFixture(t, "p1-create-send-complete.ndjson")
	want := []ports.ChatEventKind{
		ports.ChatEventTurnStarted,
		ports.ChatEventActivityStarted, ports.ChatEventReasoningDelta,
		ports.ChatEventActivityCompleted, ports.ChatEventMessageDelta,
	}
	got := kinds(events)
	if len(got) < len(want) || !reflect.DeepEqual(got[:len(want)], want) {
		t.Fatalf("events = %v, want prefix %v", got, want)
	}
	tail := []ports.ChatEventKind{ports.ChatEventMessageCompleted, ports.ChatEventUsage, ports.ChatEventTurnCompleted}
	if !reflect.DeepEqual(got[len(got)-len(tail):], tail) {
		t.Fatalf("events = %v, want suffix %v", got, tail)
	}
	if n := countKind(events, ports.ChatEventTurnStarted); n != 1 {
		t.Fatalf("turn.started count = %d", n)
	}
	if n := countKind(events, ports.ChatEventTurnCompleted); n != 1 {
		t.Fatalf("turn.completed count = %d", n)
	}
	started := events[0]
	if started.ProviderTurnID != "minted-1" || started.NativeTurnID != "" {
		t.Fatalf("turn.started = %+v (prompt_started carries no native id)", started)
	}
	var text string
	var reply ports.ChatEvent
	for _, ev := range events {
		switch ev.Kind {
		case ports.ChatEventMessageDelta:
			text += ev.Delta
		case ports.ChatEventMessageCompleted:
			reply = ev
		case ports.ChatEventReasoningDelta:
			if !strings.HasPrefix(ev.ProviderItemID, "zcode-reasoning:msg_") {
				t.Fatalf("reasoning item = %q, want keyed by assistantMessageId", ev.ProviderItemID)
			}
		}
	}
	if text != "OK" || reply.Text != "OK" || !strings.HasPrefix(reply.ProviderItemID, "msg_") {
		t.Fatalf("streamed %q, committed %+v", text, reply)
	}
	completed := events[len(events)-1]
	if completed.TurnState != domain.TurnStateCompleted || completed.NativeTurnID != "turn_be5eae50-2260-43ca-bf5d-1daa514d20e1" || completed.Err != nil {
		t.Fatalf("turn.completed = %+v", completed)
	}
}

// Tool use (p5-approval-allow.ndjson): tool_input_* and tool_call feed the
// tracker, scheduled/started open the row with the remembered input, result
// (which carries no toolName) completes it as a command with its output and
// exit code, batch emits nothing, and the resolution clears the approval.
func TestP5FixtureProducesTheToolLifecycle(t *testing.T) {
	events := normalizeFixture(t, "p5-approval-allow.ndjson")
	var started, completed []ports.ChatEvent
	for _, ev := range events {
		if ev.ProviderItemID != "call_f691a8ba78244f5391326953" {
			continue
		}
		switch ev.Kind {
		case ports.ChatEventActivityStarted:
			started = append(started, ev)
		case ports.ChatEventActivityCompleted:
			completed = append(completed, ev)
		}
	}
	// operation-event tool-scheduled, tool.updated scheduled, operation-event
	// tool-started, tool.updated started: four starts on one item, all folded.
	if len(started) != 4 {
		t.Fatalf("activity.started count = %d: %+v", len(started), started)
	}
	for _, ev := range started {
		if ev.ActivityKind != domain.ActivityKindCommand || ev.Summary != "Run date" || !strings.Contains(string(ev.Detail), `"command":"date"`) {
			t.Fatalf("activity.started = %+v (input must come from model.streaming tool_call)", ev)
		}
	}
	if len(completed) != 1 {
		t.Fatalf("activity.completed count = %d: %+v", len(completed), completed)
	}
	result := completed[0]
	if result.ActivityKind != domain.ActivityKindCommand || result.ActivityStatus != domain.ActivityStatusCompleted || result.Summary != "Run date" {
		t.Fatalf("activity.completed = %+v (toolName is not on the result frame)", result)
	}
	for _, want := range []string{`"toolName":"Bash"`, `"exitCode":0`, `"durationMs":597`, `"output":"Mon Oct 19`} {
		if !strings.Contains(string(result.Detail), want) {
			t.Fatalf("detail %s lacks %s", result.Detail, want)
		}
	}
	resolved := -1
	for i, ev := range events {
		if ev.Kind == ports.ChatEventApprovalResolved {
			resolved = i
			if ev.RequestID != "perm_97f65e85-d752-4fcd-bb8e-be66c1cd9170" || !strings.Contains(string(ev.Detail), `"decision":"allow"`) {
				t.Fatalf("approval.resolved = %+v", ev)
			}
		}
	}
	if resolved < 0 {
		t.Fatalf("no approval.resolved among %v", kinds(events))
	}
	last := events[len(events)-1]
	if last.Kind != ports.ChatEventTurnCompleted || last.TurnState != domain.TurnStateCompleted {
		t.Fatalf("last event = %+v", last)
	}
	if countKind(events, ports.ChatEventMessageCompleted) != 1 || countKind(events, ports.ChatEventUsage) != 1 {
		t.Fatalf("events = %v", kinds(events))
	}
}

// Interrupt (p4-stop.ndjson): session/stop settles the turn as interrupted
// through turn.completed resultType:"cancelled" with an empty committed
// response, and the operation-event/prompt_failed frames that follow settle
// nothing more. The streamed partial reply stays streamed: no message.completed
// is emitted for the empty response, because the store's settle would overwrite
// the row's text with "" and the timeline would show an empty assistant message
// for every interrupted turn.
func TestP4FixtureSettlesACancelledTurnAsInterrupted(t *testing.T) {
	events := normalizeFixture(t, "p4-stop.ndjson")
	if countKind(events, ports.ChatEventTurnCompleted) != 1 {
		t.Fatalf("events = %v", kinds(events))
	}
	if countKind(events, ports.ChatEventMessageDelta) == 0 {
		t.Fatalf("no text streamed before the stop: %v", kinds(events))
	}
	var completed ports.ChatEvent
	for _, ev := range events {
		switch ev.Kind {
		case ports.ChatEventTurnCompleted:
			completed = ev
		case ports.ChatEventMessageCompleted:
			t.Fatalf("message.completed %+v emitted for a cancelled turn; it would erase the streamed text", ev)
		}
	}
	if completed.TurnState != domain.TurnStateInterrupted || completed.Err != nil {
		t.Fatalf("turn.completed = %+v, want interrupted", completed)
	}
	if countKind(events, ports.ChatEventUsage) != 0 {
		t.Fatal("a cancelled turn carried usage")
	}
}

// An empty committed response never settles a row: with text streamed, a
// cancelled turn.completed emits the settlement alone, and with nothing
// streamed there is no row to settle either way.
func TestEmptyResponseEmitsNoMessageCompleted(t *testing.T) {
	tracker := armedTracker(t)
	delta := normalize(tracker, sessionEvent("model.streaming", "t-native",
		`{"kind":"text_delta","assistantMessageId":"msg-1","delta":"Here"}`))
	if len(delta) != 1 || delta[0].Kind != ports.ChatEventMessageDelta || delta[0].ProviderItemID != "msg-1" {
		t.Fatalf("text_delta = %+v", delta)
	}
	events := normalize(tracker, sessionEvent("turn.completed", "t-native", `{"resultType":"cancelled","response":""}`))
	if len(events) != 1 || events[0].Kind != ports.ChatEventTurnCompleted || events[0].TurnState != domain.TurnStateInterrupted {
		t.Fatalf("events = %+v, want the interrupted settlement alone", events)
	}
}

// A reply split around a tool call (p5-two-messages.ndjson): the turn streams
// text on one assistantMessageId, runs a tool, then streams more text on a
// second one, and turn.completed.response repeats only the second message.
// Each message keeps its own row, keyed by its assistantMessageId like the
// history import, and the committed text settles the second row only, so the
// first message is neither overwritten nor folded into the second.
func TestP5TwoMessagesFixtureKeepsOneRowPerAssistantMessage(t *testing.T) {
	events := normalizeFixture(t, "p5-two-messages.ndjson")
	const first = "msg_mvfaa6p7_b8985096-28ad-46c9-aa6f-dd04f3dd69e6"
	const second = "msg_mvfaatza_ca8bc6f9-3852-4255-9ccc-be9b13a6ea85"
	text := map[string]string{}
	var order []string
	var reply ports.ChatEvent
	for _, ev := range events {
		switch ev.Kind {
		case ports.ChatEventMessageDelta:
			if _, seen := text[ev.ProviderItemID]; !seen {
				order = append(order, ev.ProviderItemID)
			}
			text[ev.ProviderItemID] += ev.Delta
		case ports.ChatEventMessageCompleted:
			if reply.Kind != "" {
				t.Fatalf("second message.completed: %+v", ev)
			}
			reply = ev
		}
	}
	if !reflect.DeepEqual(order, []string{first, second}) {
		t.Fatalf("delta items = %v, want one row per assistant message", order)
	}
	if text[first] != "I'll run those commands now." {
		t.Fatalf("first message streamed %q", text[first])
	}
	if len(text[second]) != 179 || !strings.HasPrefix(text[second], "The `date` command ran successfully") {
		t.Fatalf("second message streamed %q", text[second])
	}
	if reply.ProviderItemID != second || reply.Text != text[second] {
		t.Fatalf("message.completed = %+v, want the second message's row with exactly its streamed text", reply)
	}
	// Both reasoning rows open and close on their own message.
	var reasoning []string
	for _, ev := range events {
		if ev.Kind == ports.ChatEventActivityCompleted && ev.ActivityKind == domain.ActivityKindReasoning {
			reasoning = append(reasoning, ev.ProviderItemID)
		}
	}
	if !reflect.DeepEqual(reasoning, []string{reasoningItem(first, ""), reasoningItem(second, "")}) {
		t.Fatalf("reasoning rows completed = %v", reasoning)
	}
	tail := []ports.ChatEventKind{ports.ChatEventMessageCompleted, ports.ChatEventUsage, ports.ChatEventTurnCompleted}
	got := kinds(events)
	if !reflect.DeepEqual(got[len(got)-len(tail):], tail) || events[len(events)-1].TurnState != domain.TurnStateCompleted {
		t.Fatalf("events = %v, want suffix %v", got, tail)
	}
}

// A model selection without a reasoning level (p7-models-level-required.ndjson):
// the send is accepted, prompt_started opens the turn, and turn.failed settles
// it failed with the provider's invalid_model_request code; the operation-event
// and prompt_failed frames settle nothing more.
func TestP7LevelRequiredFixtureFailsTheTurnWithInvalidModelRequest(t *testing.T) {
	events := normalizeFixture(t, "p7-models-level-required.ndjson")
	if !reflect.DeepEqual(kinds(events), []ports.ChatEventKind{ports.ChatEventTurnStarted, ports.ChatEventTurnCompleted}) {
		t.Fatalf("events = %v", kinds(events))
	}
	completed := events[1]
	if completed.TurnState != domain.TurnStateFailed || completed.Err == nil {
		t.Fatalf("turn.completed = %+v", completed)
	}
	for _, want := range []string{"Reasoning level is required", "code: invalid_model_request"} {
		if !strings.Contains(completed.Err.Error(), want) {
			t.Fatalf("failure %q lacks %q", completed.Err.Error(), want)
		}
	}
}

// Provider failure (p1-turn-failed-provider.ndjson): turn.failed settles the
// turn failed with the provider's code, message, detail and attribution, and
// no auth cause.
func TestTurnFailedFixtureCarriesCodeMessageDetailAndAttribution(t *testing.T) {
	events := normalizeFixture(t, "p1-turn-failed-provider.ndjson")
	if countKind(events, ports.ChatEventTurnCompleted) != 1 {
		t.Fatalf("events = %v", kinds(events))
	}
	completed := events[len(events)-1]
	if completed.Kind != ports.ChatEventTurnCompleted || completed.TurnState != domain.TurnStateFailed || completed.Err == nil {
		t.Fatalf("last event = %+v", completed)
	}
	text := completed.Err.Error()
	for _, want := range []string{"[1113][Insufficient balance", "Turn execution failed", "provider=zai-api-ao provider_code=1113", "status=429", "retryable=false"} {
		if !strings.Contains(text, want) {
			t.Fatalf("failure %q lacks %q", text, want)
		}
	}
	if errors.Is(completed.Err, ports.ErrChatAuthRequired) {
		t.Fatal("turn.failed attached an auth cause without a proven native signal")
	}
}

func TestProviderFailureRendering(t *testing.T) {
	cases := []struct {
		name string
		err  zcodeproto.ProtocolErrorDetail
		want []string
	}{
		{
			name: "configuration error (run 1)",
			err:  zcodeproto.ProtocolErrorDetail{Type: "unknown_error", Code: "CONFIGURATION_ERROR", Message: "Select a model before continuing", Detail: "Model creation failed", Attribution: &zcodeproto.ErrorAttribution{Retryable: false}},
			want: []string{"Select a model before continuing", "Model creation failed", "code: CONFIGURATION_ERROR"},
		},
		{
			name: "invalid model request (p7)",
			err:  zcodeproto.ProtocolErrorDetail{Code: "invalid_model_request", Message: "Reasoning level is required for zai-api-ao/GLM-5.3", Detail: "Model creation failed"},
			want: []string{"Reasoning level is required", "code: invalid_model_request"},
		},
		{
			name: "attribution without a detail line",
			err:  zcodeproto.ProtocolErrorDetail{Code: "1113", Message: "[1113][quota]", Attribution: &zcodeproto.ErrorAttribution{Source: "provider", StatusCode: 429, ProviderErrorCode: "1113"}},
			want: []string{"[1113][quota]", "attribution: source=provider status=429 providerErrorCode=1113 retryable=false"},
		},
		{
			name: "message falls back to the underlying one",
			err:  zcodeproto.ProtocolErrorDetail{UnderlyingErrorMessage: "boom"},
			want: []string{"boom"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text := providerFailure(tc.err).Error()
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Fatalf("%q lacks %q", text, want)
				}
			}
			if strings.Contains(text, "code: 1113") {
				t.Fatalf("%q repeats a code the message already carries", text)
			}
		})
	}
}

func TestNormalizeTable(t *testing.T) {
	cases := []struct {
		name  string
		n     notification
		kind  ports.ChatEventKind
		check func(t *testing.T, ev ports.ChatEvent)
	}{
		{
			name: "text delta via model.streaming",
			n:    sessionEvent("model.streaming", "t-native", `{"kind":"text_delta","delta":"He","assistantMessageId":"msg-1","done":false}`),
			kind: ports.ChatEventMessageDelta,
			check: func(t *testing.T, ev ports.ChatEvent) {
				if ev.Delta != "He" || ev.ProviderItemID != "msg-1" || ev.ProviderTurnID != "minted-1" {
					t.Fatalf("message.delta = %+v", ev)
				}
			},
		},
		{
			name: "first reasoning delta opens the row",
			n:    sessionEvent("model.streaming", "t-native", `{"kind":"reasoning_delta","assistantMessageId":"msg-1","delta":"hmm"}`),
			kind: ports.ChatEventActivityStarted,
			check: func(t *testing.T, ev ports.ChatEvent) {
				if ev.ActivityKind != domain.ActivityKindReasoning || ev.ProviderItemID != "zcode-reasoning:msg-1" || ev.Summary != "Reasoning" {
					t.Fatalf("activity.started = %+v", ev)
				}
			},
		},
		{
			name: "reasoning delta",
			n:    sessionEvent("model.streaming", "t-native", `{"kind":"reasoning_delta","assistantMessageId":"msg-1","delta":"hmm"}`),
			kind: ports.ChatEventReasoningDelta,
			check: func(t *testing.T, ev ports.ChatEvent) {
				if ev.Delta != "hmm" || ev.ProviderItemID != "zcode-reasoning:msg-1" {
					t.Fatalf("reasoning.delta = %+v", ev)
				}
			},
		},
		{
			name: "STATIC reasoning_start opens the row",
			n:    sessionEvent("model.streaming", "t-native", `{"kind":"reasoning_start","partId":"p1"}`),
			kind: ports.ChatEventActivityStarted,
			check: func(t *testing.T, ev ports.ChatEvent) {
				if ev.ActivityKind != domain.ActivityKindReasoning || ev.ProviderItemID != "zcode-reasoning:minted-1" {
					t.Fatalf("activity.started = %+v", ev)
				}
			},
		},
		{
			name: "tool scheduled with inline input",
			n:    sessionEvent("tool.updated", "t-native", `{"kind":"scheduled","toolCallId":"tc1","toolName":"Bash","input":{"command":"date"}}`),
			kind: ports.ChatEventActivityStarted,
			check: func(t *testing.T, ev ports.ChatEvent) {
				if ev.ProviderItemID != "tc1" || ev.ActivityKind != domain.ActivityKindCommand || ev.ActivityStatus != domain.ActivityStatusRunning {
					t.Fatalf("activity.started = %+v", ev)
				}
				if ev.Summary != "Run date" || !strings.Contains(string(ev.Detail), `"command":"date"`) {
					t.Fatalf("summary/detail = %q / %s", ev.Summary, ev.Detail)
				}
			},
		},
		{
			name: "file tool is a file change",
			n:    sessionEvent("tool.updated", "t-native", `{"kind":"started","toolCallId":"tc2","toolName":"Edit","input":{"file_path":"a.go"}}`),
			kind: ports.ChatEventActivityStarted,
			check: func(t *testing.T, ev ports.ChatEvent) {
				if ev.ActivityKind != domain.ActivityKindFileChange {
					t.Fatalf("kind = %q", ev.ActivityKind)
				}
			},
		},
		{
			name: "mcp tool carries its server",
			n:    sessionEvent("tool.updated", "t-native", `{"kind":"started","toolCallId":"tc3","toolName":"mcp__ao__html_preview"}`),
			kind: ports.ChatEventActivityStarted,
			check: func(t *testing.T, ev ports.ChatEvent) {
				if ev.ActivityKind != domain.ActivityKindMCPTool || !strings.Contains(string(ev.Detail), `"server":"ao"`) {
					t.Fatalf("mcp activity = %+v", ev)
				}
			},
		},
		{
			name: "failed tool result fails the activity",
			n:    sessionEvent("tool.updated", "t-native", `{"kind":"result","toolCallId":"tc1","result":{"success":false,"content":"boom"},"duration":12}`),
			kind: ports.ChatEventActivityCompleted,
			check: func(t *testing.T, ev ports.ChatEvent) {
				if ev.ActivityStatus != domain.ActivityStatusFailed || !strings.Contains(string(ev.Detail), `"durationMs":12`) || !strings.Contains(string(ev.Detail), `"output":"boom"`) {
					t.Fatalf("activity.completed = %+v", ev)
				}
			},
		},
		{
			name: "STATIC tool error fails the activity",
			n:    sessionEvent("tool.updated", "t-native", `{"kind":"error","toolCallId":"tc1","toolName":"Bash","error":{"message":"boom"}}`),
			kind: ports.ChatEventActivityCompleted,
			check: func(t *testing.T, ev ports.ChatEvent) {
				if ev.ActivityStatus != domain.ActivityStatusFailed || !strings.Contains(string(ev.Detail), "boom") {
					t.Fatalf("activity.completed = %+v", ev)
				}
			},
		},
		{
			name: "operation-event tool fallback",
			n:    notif(zcodeproto.MethodComputerUseOperationEvent, `{"kind":"tool-started","turnId":"t-native","toolCallId":"tc9","toolName":"Bash"}`),
			kind: ports.ChatEventActivityStarted,
			check: func(t *testing.T, ev ports.ChatEvent) {
				if ev.ProviderItemID != "tc9" || ev.ProviderTurnID != "minted-1" {
					t.Fatalf("activity.started = %+v", ev)
				}
			},
		},
		{
			name: "turn failed carries the provider prose with no auth cause",
			n:    sessionEvent("turn.failed", "t-native", `{"error":{"type":"AiSdkModelAdapterError","message":"model request failed","detail":"401 from upstream"},"turnPhase":"prepare"}`),
			kind: ports.ChatEventTurnCompleted,
			check: func(t *testing.T, ev ports.ChatEvent) {
				if ev.TurnState != domain.TurnStateFailed || ev.Err == nil || !strings.Contains(ev.Err.Error(), "model request failed") {
					t.Fatalf("turn.completed = %+v", ev)
				}
				if ev.NativeTurnID != "t-native" {
					t.Fatalf("native id = %q", ev.NativeTurnID)
				}
				if errors.Is(ev.Err, ports.ErrChatAuthRequired) {
					t.Fatal("turn.failed attached an auth cause without a proven native signal")
				}
			},
		},
		{
			name: "turn completed with usage",
			n:    sessionEvent("turn.completed", "t-native", `{"response":"OK","resultType":"success","usage":{"source":"provider","inputTokens":10,"outputTokens":2,"cacheReadTokens":1,"totalTokens":12}}`),
			kind: ports.ChatEventUsage,
			check: func(t *testing.T, ev ports.ChatEvent) {
				// Totals only: the context group comes from the pump's post-turn
				// session/read (TestUsagePollReadsAFreshSnapshotForContext), never
				// from the inline usage.
				if ev.Usage == nil || ev.Usage.InputTokens != 10 || ev.Usage.CachedTokens != 1 || !ev.Usage.TotalsKnown || ev.Usage.ContextKnown {
					t.Fatalf("usage = %+v", ev.Usage)
				}
			},
		},
		{
			name: "cancelled result type is interrupted",
			n:    sessionEvent("turn.completed", "t-native", `{"resultType":"cancelled","response":""}`),
			kind: ports.ChatEventTurnCompleted,
			check: func(t *testing.T, ev ports.ChatEvent) {
				if ev.TurnState != domain.TurnStateInterrupted {
					t.Fatalf("state = %q", ev.TurnState)
				}
			},
		},
		{
			name: "title updated",
			n:    sessionEvent("session.titleUpdated", "", `{"title":"Fix the build","previousTitle":"","source":"first_input"}`),
			kind: ports.ChatEventThreadRenamed,
			check: func(t *testing.T, ev ports.ChatEvent) {
				if ev.Title != "Fix the build" {
					t.Fatalf("title = %q", ev.Title)
				}
			},
		},
		{
			name: "STATIC session closed is recorded, never torn down",
			n:    sessionEvent("session.closed", "", `{}`),
			kind: ports.ChatEventThreadState,
			check: func(t *testing.T, ev ports.ChatEvent) {
				if ev.ThreadState == nil || !ev.ThreadState.Closed || ev.ThreadState.Status != domain.ThreadStatusClosed {
					t.Fatalf("thread.state = %+v", ev.ThreadState)
				}
			},
		},
		{
			name: "STATIC operation-event session-closed",
			n:    notif(zcodeproto.MethodComputerUseOperationEvent, `{"kind":"session-closed","sessionId":"sess-1"}`),
			kind: ports.ChatEventThreadState,
			check: func(t *testing.T, ev ports.ChatEvent) {
				if ev.ThreadState == nil || !ev.ThreadState.Closed {
					t.Fatalf("thread.state = %+v", ev.ThreadState)
				}
			},
		},
		{
			name: "STATIC runtime headers cancelled is an error event",
			n:    notif(zcodeproto.MethodInteractionProviderRuntimeHeadersCancelled, `{"sessionId":"sess-1"}`),
			kind: ports.ChatEventError,
			check: func(t *testing.T, ev ports.ChatEvent) {
				if ev.Err == nil || !strings.Contains(ev.Err.Error(), "timed out") {
					t.Fatalf("error = %v", ev.Err)
				}
			},
		},
		{
			name: "STATIC user input resolved",
			n:    sessionEvent("userInput.resolved", "t-native", `{"requestId":"q1","action":"accept"}`),
			kind: ports.ChatEventInputResolved,
			check: func(t *testing.T, ev ports.ChatEvent) {
				if ev.RequestID != "q1" {
					t.Fatalf("input.resolved = %+v", ev)
				}
			},
		},
		{
			name: "permission resolved with requestId",
			n:    sessionEvent("permission.resolved", "t-native", `{"requestId":"r1","toolCallId":"tc1","decision":"allow","reason":"Approved once"}`),
			kind: ports.ChatEventApprovalResolved,
			check: func(t *testing.T, ev ports.ChatEvent) {
				if ev.RequestID != "r1" || !strings.Contains(string(ev.Detail), `"toolCallId":"tc1"`) {
					t.Fatalf("approval.resolved = %+v", ev)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tracker := armedTracker(t)
			events := normalize(tracker, tc.n)
			var found *ports.ChatEvent
			for i := range events {
				if events[i].Kind == tc.kind {
					found = &events[i]
					break
				}
			}
			if found == nil {
				t.Fatalf("no %q among %+v", tc.kind, events)
			}
			tc.check(t, *found)
		})
	}
}

func TestReasoningRowOpensOnceAndClosesWhenTheMessageMovesOn(t *testing.T) {
	tracker := armedTracker(t)
	first := normalize(tracker, sessionEvent("model.streaming", "t-native", `{"kind":"reasoning_delta","assistantMessageId":"m1","delta":"a"}`))
	if !reflect.DeepEqual(kinds(first), []ports.ChatEventKind{ports.ChatEventActivityStarted, ports.ChatEventReasoningDelta}) {
		t.Fatalf("first reasoning delta = %v", kinds(first))
	}
	second := normalize(tracker, sessionEvent("model.streaming", "t-native", `{"kind":"reasoning_delta","assistantMessageId":"m1","delta":"b"}`))
	if !reflect.DeepEqual(kinds(second), []ports.ChatEventKind{ports.ChatEventReasoningDelta}) {
		t.Fatalf("second reasoning delta = %v", kinds(second))
	}
	// A tool call closes the row (p5: reasoning, then tool_input_start).
	closed := normalize(tracker, sessionEvent("model.streaming", "t-native", `{"kind":"tool_input_start","assistantMessageId":"m1","toolCallId":"tc1","toolName":"Bash"}`))
	if len(closed) != 1 || closed[0].Kind != ports.ChatEventActivityCompleted || closed[0].ProviderItemID != "zcode-reasoning:m1" || closed[0].ActivityStatus != domain.ActivityStatusCompleted {
		t.Fatalf("tool_input_start = %+v", closed)
	}
	if got := normalize(tracker, sessionEvent("model.streaming", "t-native", `{"kind":"text_delta","assistantMessageId":"m1","delta":"x"}`)); len(got) != 1 || got[0].Kind != ports.ChatEventMessageDelta {
		t.Fatalf("text after a closed reasoning row = %v", kinds(got))
	}
	// The tool name learned on tool_input_start serves the result frame.
	if got := normalize(tracker, sessionEvent("tool.updated", "t-native", `{"kind":"result","toolCallId":"tc1","result":{"success":true,"content":"ok"},"duration":1}`)); len(got) != 1 || !strings.Contains(string(got[0].Detail), `"toolName":"Bash"`) {
		t.Fatalf("result after tool_input_start = %+v", got)
	}
}

func TestTextSourceLatchPreventsDoubleText(t *testing.T) {
	tracker := armedTracker(t)
	first := normalize(tracker, sessionEvent("model.streaming", "t-native", `{"kind":"text_delta","delta":"a","assistantMessageId":"m1"}`))
	if len(first) != 1 {
		t.Fatalf("model.streaming delta = %+v", first)
	}
	if got := normalize(tracker, sessionEvent("part.delta", "t-native", `{"messageId":"m1","partId":"p1","field":"text","delta":"a"}`)); len(got) != 0 {
		t.Fatalf("part.delta doubled the text: %+v", got)
	}
	completed := normalize(tracker, sessionEvent("turn.completed", "t-native", `{"response":"a","resultType":"success"}`))
	if len(completed) != 2 || completed[0].Kind != ports.ChatEventMessageCompleted || completed[0].ProviderItemID != "m1" || completed[1].Kind != ports.ChatEventTurnCompleted {
		t.Fatalf("turn.completed events = %+v", completed)
	}

	// A new turn latches afresh, and part.delta is honoured when it comes first.
	tracker.prepare("minted-2")
	normalize(tracker, notif(zcodeproto.MethodStateUpdated, `{"reason":"prompt_started"}`))
	if got := normalize(tracker, sessionEvent("part.delta", "", `{"messageId":"m2","field":"text","delta":"b"}`)); len(got) != 1 || got[0].ProviderItemID != "m2" || got[0].ProviderTurnID != "minted-2" {
		t.Fatalf("part.delta on a fresh turn = %+v", got)
	}
	if got := normalize(tracker, sessionEvent("model.streaming", "", `{"kind":"text_delta","delta":"b"}`)); len(got) != 0 {
		t.Fatalf("model.streaming doubled the text: %+v", got)
	}
}

func TestProgressTailSuffixDiff(t *testing.T) {
	tracker := armedTracker(t)
	progress := func(tail string) []ports.ChatEvent {
		return normalize(tracker, sessionEvent("tool.updated", "t-native", `{"kind":"progress","toolCallId":"tc1","stdoutTail":`+strconvQuote(tail)+`}`))
	}
	if got := progress("abc"); len(got) != 1 || got[0].Delta != "abc" || got[0].Kind != ports.ChatEventCommandOutputDelta {
		t.Fatalf("first progress = %+v", got)
	}
	if got := progress("abcdef"); len(got) != 1 || got[0].Delta != "def" {
		t.Fatalf("extension = %+v", got)
	}
	if got := progress("abcdef"); len(got) != 0 {
		t.Fatalf("unchanged tail emitted: %+v", got)
	}
	if got := progress("zzz"); len(got) != 0 {
		t.Fatalf("non-extending tail emitted (suffix-diff rule): %+v", got)
	}

	// stdout and stderr are diffed independently: concatenated, a growing
	// stdout would shift stderr and no later tail would extend the last one.
	both := func(stdout, stderr string) []string {
		var deltas []string
		for _, ev := range normalize(tracker, sessionEvent("tool.updated", "t-native",
			`{"kind":"progress","toolCallId":"tc2","stdoutTail":`+strconvQuote(stdout)+`,"stderrTail":`+strconvQuote(stderr)+`}`)) {
			if ev.Kind != ports.ChatEventCommandOutputDelta || ev.ProviderItemID != "tc2" {
				t.Fatalf("progress event = %+v", ev)
			}
			deltas = append(deltas, ev.Delta)
		}
		return deltas
	}
	for _, step := range []struct {
		stdout, stderr string
		want           []string
	}{
		{"ab", "e", []string{"ab", "e"}},
		{"abc", "e", []string{"c"}},
		{"abc", "ef", []string{"f"}},
		{"abcd", "efg", []string{"d", "g"}},
		{"abcd", "efg", nil},
	} {
		if got := both(step.stdout, step.stderr); !reflect.DeepEqual(got, step.want) {
			t.Fatalf("progress(%q, %q) = %v, want %v", step.stdout, step.stderr, got, step.want)
		}
	}
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestNormalizeToleratesMalformedParams(t *testing.T) {
	methods := []string{
		zcodeproto.MethodStateUpdated,
		zcodeproto.MethodComputerUseOperationEvent,
		zcodeproto.MethodSessionEvent,
		zcodeproto.MethodInteractionProviderRuntimeHeadersCancelled,
	}
	for _, method := range methods {
		for _, params := range []string{`"not an object"`, `42`, `[1,2]`, `null`, ``, `{"type":"turn.completed","payload":"nope"}`, `{"type":"tool.updated","payload":[]}`, `{"type":"model.streaming","payload":7}`, `{"type":"turn.failed","payload":{"error":"x"}}`, `{"kind":7}`} {
			tracker := armedTracker(t)
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("%s with params %s panicked: %v", method, params, r)
					}
				}()
				_ = normalize(tracker, notif(method, params))
			}()
		}
	}
	// The runtime-headers cancellation is the one method whose params carry
	// nothing the event needs; everything else yields nil on garbage.
	tracker := armedTracker(t)
	for _, method := range methods[:3] {
		if got := normalize(tracker, notif(method, `"not an object"`)); got != nil {
			t.Fatalf("%s with garbage params emitted %+v", method, got)
		}
	}
}

func TestNoiseIsDropped(t *testing.T) {
	tracker := armedTracker(t)
	noise := []struct {
		tag string
		n   notification
	}{
		{"PROVEN p1", notif(zcodeproto.MethodStartupStorageState, `{"phase":"waiting_for_lock"}`)},
		{"PROVEN p1", notif(zcodeproto.MethodProcessMcpTelemetry, `{"kind":"process_start"}`)},
		{"PROVEN p1", notif(zcodeproto.MethodV4TelemetryEvent, `{"kind":"turn.started","sourceCommandId":"minted-1"}`)},
		{"STATIC sT", notif(zcodeproto.MethodProcessResourceSample, `{"rss":1}`)},
		{"STATIC sT", notif(zcodeproto.MethodProcessMcpResourceSamples, `{}`)},
		{"STATIC sT", notif(zcodeproto.MethodProcessToolExecResource, `{}`)},
		{"STATIC sT", notif(zcodeproto.MethodProcessNetworkRequests, `{}`)},
		{"STATIC sT", notif(zcodeproto.MethodPluginsOperationProgress, `{}`)},
		{"STATIC Sg", notif("v4/desktop/anything", `{}`)},
		{"PROVEN p1", sessionEvent("session.updated", "t-native", `{}`)},
		{"PROVEN p5", sessionEvent("streamRecovery.updated", "t-native", `{"kind":"tool_result"}`)},
		{"PROVEN p5", sessionEvent("permission.requested", "t-native", `{"requestId":"r1","toolCallId":"tc1"}`)},
		{"PROVEN p5", sessionEvent("tool.updated", "t-native", `{"kind":"batch","toolCallIds":["tc1"],"successCount":1,"errorCount":0}`)},
		{"PROVEN p5", sessionEvent("model.streaming", "t-native", `{"kind":"tool_input_delta","toolCallId":"tc1","delta":"{\"command\":\"date\"}"}`)},
		{"PROVEN p5", sessionEvent("model.streaming", "t-native", `{"kind":"tool_input_end","toolCallId":"tc1"}`)},
		{"PROVEN p5", sessionEvent("model.streaming", "t-native", `{"kind":"tool_call","toolCallId":"tc1","toolName":"Bash","input":{"command":"date"}}`)},
		{"STATIC zXt", sessionEvent("checkpoint.created", "t-native", `{}`)},
		{"STATIC zXt", sessionEvent("rewind.triggered", "t-native", `{}`)},
		{"STATIC zXt", sessionEvent("turn.steerQueued", "t-native", `{}`)},
		{"STATIC zXt", sessionEvent("turn.steerDrained", "t-native", `{}`)},
		{"STATIC zXt", sessionEvent("message.created", "t-native", `{}`)},
		{"STATIC zXt", sessionEvent("part.started", "t-native", `{}`)},
		{"STATIC zXt", sessionEvent("part.upserted", "t-native", `{}`)},
		{"STATIC zXt", sessionEvent("part.removed", "t-native", `{}`)},
		{"PROVEN p7", notif(zcodeproto.MethodStateUpdated, `{"reason":"model_changed","patch":{}}`)},
		{"STATIC Lhc/Xmc", notif(zcodeproto.MethodStateUpdated, `{"reason":"settings_updated"}`)},
		{"STATIC Oai", notif(zcodeproto.MethodComputerUseOperationEvent, `{"kind":"heartbeat"}`)},
	}
	for _, entry := range noise {
		if got := normalize(tracker, entry.n); len(got) != 0 {
			t.Fatalf("%s (%s) emitted %+v", entry.n.Method, entry.tag, got)
		}
	}
	if tracker.currentTurn() != "minted-1" {
		t.Fatal("noise settled the turn")
	}
	// The dropped tool_call was still remembered for the row that follows.
	if got := normalize(tracker, sessionEvent("tool.updated", "t-native", `{"kind":"scheduled","toolCallId":"tc1","toolName":"Bash","inputOmitted":true,"inputRef":"model_stream"}`)); len(got) != 1 || got[0].Summary != "Run date" {
		t.Fatalf("scheduled after tool_call = %+v", got)
	}
}

func TestUnknownResultTypeFailsClosed(t *testing.T) {
	for _, resultType := range []string{"", "error_timeout", "something_new"} {
		tracker := armedTracker(t)
		events := normalize(tracker, sessionEvent("turn.completed", "t-native", `{"resultType":`+strconvQuote(resultType)+`}`))
		if len(events) != 1 || events[0].Kind != ports.ChatEventTurnCompleted {
			t.Fatalf("%q: events = %+v", resultType, events)
		}
		if events[0].TurnState != domain.TurnStateFailed || events[0].Err == nil {
			t.Fatalf("%q settled as %q (err %v), want failed with an error", resultType, events[0].TurnState, events[0].Err)
		}
	}
}

func TestTurnLifecycleDedupesAcrossThreeSignals(t *testing.T) {
	signals := map[string]notification{
		"prompt_started": notif(zcodeproto.MethodStateUpdated, `{"reason":"prompt_started"}`),
		"operation":      notif(zcodeproto.MethodComputerUseOperationEvent, `{"kind":"turn-started","turnId":"t-native"}`),
		"session/event":  sessionEvent("turn.started", "t-native", `{"inputId":"minted-1","turnNumber":1}`),
	}
	orders := [][]string{
		{"prompt_started", "operation", "session/event"},
		{"prompt_started", "session/event", "operation"},
		{"operation", "prompt_started", "session/event"},
		{"operation", "session/event", "prompt_started"},
		{"session/event", "prompt_started", "operation"},
		{"session/event", "operation", "prompt_started"},
	}
	for _, order := range orders {
		t.Run(strings.Join(order, ">"), func(t *testing.T) {
			tracker := newTurnTracker()
			tracker.prepare("minted-1")
			var started []ports.ChatEvent
			for _, name := range order {
				for _, ev := range normalize(tracker, signals[name]) {
					if ev.Kind == ports.ChatEventTurnStarted {
						started = append(started, ev)
					}
				}
			}
			if len(started) != 1 || started[0].ProviderTurnID != "minted-1" {
				t.Fatalf("turn.started = %+v", started)
			}
			if order[0] != "prompt_started" && started[0].NativeTurnID != "t-native" {
				t.Fatalf("native id missing on a native-carrying first signal: %+v", started[0])
			}
			if tracker.nativeFor("minted-1") != "t-native" || tracker.mintedFor("t-native") != "minted-1" {
				t.Fatal("native mapping not learned")
			}
			completed := normalize(tracker, notif(zcodeproto.MethodStateUpdated, `{"reason":"prompt_completed"}`))
			if len(completed) != 1 || completed[0].NativeTurnID != "t-native" || completed[0].TurnState != domain.TurnStateCompleted {
				t.Fatalf("turn.completed = %+v", completed)
			}
			if got := normalize(tracker, notif(zcodeproto.MethodComputerUseOperationEvent, `{"kind":"turn-completed","turnId":"t-native"}`)); len(got) != 0 {
				t.Fatalf("settled twice: %+v", got)
			}
		})
	}
}

// The operation-event completion arrives BEFORE session/event turn.completed
// (PROVEN order in every fixture) and must not settle the turn, or a cancelled
// turn reads as completed and a failed one loses its error.
func TestOperationEventCompletionNeverSettlesTheTurn(t *testing.T) {
	for _, kind := range []string{"turn-completed", "turn-failed"} {
		tracker := armedTracker(t)
		if got := normalize(tracker, notif(zcodeproto.MethodComputerUseOperationEvent, `{"kind":"`+kind+`","turnId":"t-native"}`)); len(got) != 0 {
			t.Fatalf("%s emitted %+v", kind, got)
		}
		if tracker.currentTurn() != "minted-1" {
			t.Fatalf("%s settled the turn", kind)
		}
		completed := normalize(tracker, sessionEvent("turn.completed", "t-native", `{"resultType":"cancelled","response":""}`))
		if len(completed) != 1 || completed[0].TurnState != domain.TurnStateInterrupted {
			t.Fatalf("session/event after %s = %+v", kind, completed)
		}
	}
}

func TestProviderInitiatedTurnGetsANamespacedID(t *testing.T) {
	tracker := newTurnTracker()
	events := normalize(tracker, notif(zcodeproto.MethodComputerUseOperationEvent, `{"kind":"turn-started","turnId":"t-goal"}`))
	if len(events) != 1 || events[0].ProviderTurnID != providerTurnPrefix+"t-goal" {
		t.Fatalf("provider-initiated turn = %+v", events)
	}
	// A bare prompt_started with nothing prepared names nothing: dropped.
	if got := normalize(newTurnTracker(), notif(zcodeproto.MethodStateUpdated, `{"reason":"prompt_started"}`)); len(got) != 0 {
		t.Fatalf("uncorrelated prompt_started emitted %+v", got)
	}
	// turn.started's inputId echo names the minted id when nothing is armed
	// (a replayed start after a reconnect), so the turn correlates.
	if got := normalize(newTurnTracker(), sessionEvent("turn.started", "t-native", `{"inputId":"durable-uuid","turnNumber":1}`)); len(got) != 1 || got[0].ProviderTurnID != "durable-uuid" || got[0].NativeTurnID != "t-native" {
		t.Fatalf("turn.started with an inputId echo = %+v", got)
	}
}
