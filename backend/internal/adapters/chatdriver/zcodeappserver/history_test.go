package zcodeappserver

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/zcodeappserver/zcodeproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// p8SessionID is the session the p8-resume.ndjson snapshot belongs to.
const p8SessionID = "sess_22806715-9fb7-41f0-bbaf-0fc8ddeec062"

// p8Snapshot decodes the captured session/resume result (p8-resume.ndjson):
// a visible user prompt, a hidden runtime system_reminder, and the assistant
// reply with step-start/reasoning/text/step-finish parts.
func p8Snapshot(t *testing.T) zcodeproto.Snapshot {
	t.Helper()
	var snapshot zcodeproto.Snapshot
	if err := json.Unmarshal([]byte(fixtureResult(t, "p8-resume.ndjson", 1)), &snapshot); err != nil {
		t.Fatalf("decode p8 snapshot: %v", err)
	}
	if snapshot.Session.SessionID != p8SessionID || len(snapshot.Messages) != 3 {
		t.Fatalf("p8 snapshot = session %q, %d messages", snapshot.Session.SessionID, len(snapshot.Messages))
	}
	return snapshot
}

// p8ReadResult is the p8 snapshot re-homed on the scripted server's session
// id, as a session/read of the test conversation would return it.
func p8ReadResult(t *testing.T) string {
	t.Helper()
	return strings.ReplaceAll(fixtureResult(t, "p8-resume.ndjson", 1), p8SessionID, "sess-1")
}

func TestHistoryEventsProjectTheCapturedTranscript(t *testing.T) {
	messages := p8Snapshot(t).Messages
	user, hidden, assistant := messages[0], messages[1], messages[2]
	if hidden.Info.Semantics.UIVisibility != "hidden" || assistant.Info.Role != "assistant" {
		t.Fatalf("unexpected fixture shape: %+v / %+v", hidden.Info, assistant.Info)
	}

	events := historyEvents(p8SessionID, messages)
	want := []ports.ChatEventKind{
		ports.ChatEventTurnStarted, ports.ChatEventUserMessageCompleted,
		ports.ChatEventActivityStarted, ports.ChatEventReasoningDelta, ports.ChatEventActivityCompleted,
		ports.ChatEventMessageCompleted, ports.ChatEventTurnCompleted,
	}
	if !reflect.DeepEqual(kinds(events), want) {
		t.Fatalf("events = %v, want %v", kinds(events), want)
	}
	// One turn, keyed by the visible prompt's messageId.
	turn := user.Info.MessageID
	for _, ev := range events {
		if ev.ProviderTurnID != turn {
			t.Fatalf("event %s filed under turn %q, want %q", ev.Kind, ev.ProviderTurnID, turn)
		}
		if strings.Contains(ev.Text, "Available agent types") || strings.Contains(ev.Delta, "Available agent types") {
			t.Fatalf("hidden system_reminder leaked into %+v", ev)
		}
	}
	prompt := events[1]
	if prompt.ProviderItemID != user.Info.MessageID || prompt.Text != joinedText(user.Parts, zcodeproto.PartText) || prompt.ClientMessageID == "" {
		t.Fatalf("user message = %+v", prompt)
	}
	// Reasoning rows share the live stream's key (reasoningItem) so a replay
	// after a live turn lands on the same activity row.
	reasoningID := reasoningItem(assistant.Info.MessageID, turn)
	for _, ev := range events[2:5] {
		if ev.ProviderItemID != reasoningID || (ev.Kind != ports.ChatEventReasoningDelta && ev.ActivityKind != domain.ActivityKindReasoning) {
			t.Fatalf("reasoning event = %+v, want item %q", ev, reasoningID)
		}
	}
	if events[3].Delta != joinedText(assistant.Parts, zcodeproto.PartReasoning) || events[3].Delta == "" {
		t.Fatalf("reasoning delta = %q", events[3].Delta)
	}
	// The assistant text is keyed by its messageId, the live text_delta key.
	reply := events[5]
	if reply.ProviderItemID != assistant.Info.MessageID || reply.Text != "OK" {
		t.Fatalf("message.completed = %+v", reply)
	}
	if events[6].TurnState != domain.TurnStateCompleted {
		t.Fatalf("turn.completed = %+v", events[6])
	}
	seen := map[string]bool{}
	for _, ev := range events {
		if !strings.HasPrefix(ev.ProviderEventID, historyEventPrefix+p8SessionID+":") {
			t.Fatalf("event id %q lacks the zcode-history:<sessionId>: prefix", ev.ProviderEventID)
		}
		if seen[ev.ProviderEventID] {
			t.Fatalf("event id %q used twice", ev.ProviderEventID)
		}
		seen[ev.ProviderEventID] = true
	}
	// The same read yields the same ids.
	again := historyEvents(p8SessionID, messages)
	if len(again) != len(events) {
		t.Fatalf("historyEvents is not deterministic: %d events, then %d", len(events), len(again))
	}
	for i := range events {
		if again[i].ProviderEventID != events[i].ProviderEventID || again[i].Kind != events[i].Kind {
			t.Fatalf("historyEvents is not deterministic at %d: %+v vs %+v", i, events[i], again[i])
		}
	}
}

func TestHistoryEventsGiveAnAssistantOnlyTranscriptItsOwnTurn(t *testing.T) {
	assistant := p8Snapshot(t).Messages[2]
	events := historyEvents("sess-x", []zcodeproto.Message{assistant})
	want := []ports.ChatEventKind{
		ports.ChatEventTurnStarted,
		ports.ChatEventActivityStarted, ports.ChatEventReasoningDelta, ports.ChatEventActivityCompleted,
		ports.ChatEventMessageCompleted, ports.ChatEventTurnCompleted,
	}
	if !reflect.DeepEqual(kinds(events), want) {
		t.Fatalf("events = %v, want %v", kinds(events), want)
	}
	for _, ev := range events {
		if ev.ProviderTurnID != assistant.Info.MessageID {
			t.Fatalf("event %s filed under %q, want the assistant message's own turn", ev.Kind, ev.ProviderTurnID)
		}
	}
	// Nothing to import: hidden, empty or id-less messages yield no events.
	empty := []zcodeproto.Message{
		p8Snapshot(t).Messages[1],
		{Info: zcodeproto.MessageInfo{Role: "user", MessageID: "msg-blank"}, Parts: []zcodeproto.Part{{Type: zcodeproto.PartText, Text: "  "}}},
		{Info: zcodeproto.MessageInfo{Role: "assistant"}, Parts: []zcodeproto.Part{{Type: zcodeproto.PartText, Text: "orphan"}}},
	}
	if got := historyEvents("sess-x", empty); len(got) != 0 {
		t.Fatalf("events for nothing visible = %v", kinds(got))
	}
}

func TestReadHistoryReadsTheSessionWithTheProvenLimit(t *testing.T) {
	conv, srv := startConversation(t)
	srv.respondTo(zcodeproto.MethodSessionRead, p8ReadResult(t))
	history, err := conv.ReadHistory(context.Background())
	if err != nil {
		t.Fatalf("ReadHistory: %v", err)
	}
	read := srv.awaitRequest(zcodeproto.MethodSessionRead)
	if got := paramsMap(t, read); got["sessionId"] != "sess-1" || messageLimitOf(t, read) != historyMessageLimit {
		t.Fatalf("session/read params = %s, want sessionId sess-1 and messageLimit %d", read.Params, historyMessageLimit)
	}
	if countKind(history, ports.ChatEventUserMessageCompleted) != 1 || countKind(history, ports.ChatEventMessageCompleted) != 1 {
		t.Fatalf("history = %v", kinds(history))
	}
	for _, ev := range history {
		if !strings.HasPrefix(ev.ProviderEventID, historyEventPrefix+"sess-1:") {
			t.Fatalf("event id %q not namespaced by the conversation's session", ev.ProviderEventID)
		}
	}

	// RefreshHistory is another authoritative read, not a cached replay.
	if _, err := conv.RefreshHistory(context.Background()); err != nil {
		t.Fatalf("RefreshHistory: %v", err)
	}
	reads := srv.countFrames(func(f frame) bool {
		return f.Method == zcodeproto.MethodSessionRead && strings.Contains(string(f.Params), `"messageLimit":10000`)
	})
	if reads != 2 {
		t.Fatalf("history session/read frames = %d, want 2", reads)
	}
}

func TestReadHistoryRefusesAnUnsettledOrForeignSession(t *testing.T) {
	t.Run("running projection", func(t *testing.T) {
		conv, srv := startConversation(t)
		srv.respondTo(zcodeproto.MethodSessionRead, strings.Replace(p8ReadResult(t), `"status":"idle"`, `"status":"running"`, 1))
		if _, err := conv.ReadHistory(context.Background()); !errors.Is(err, ports.ErrChatHistoryUnsettled) {
			t.Fatalf("ReadHistory with a running projection = %v, want ErrChatHistoryUnsettled", err)
		}
	})
	t.Run("open turn", func(t *testing.T) {
		conv, srv := startConversation(t)
		srv.respondTo(zcodeproto.MethodSessionRead, p8ReadResult(t))
		sendTurn(t, conv, "hello")
		srv.push(`{"method":"state.updated","params":{"reason":"prompt_started"}}`)
		nextEvent(t, conv.Events(), ports.ChatEventTurnStarted)
		if _, err := conv.ReadHistory(context.Background()); !errors.Is(err, ports.ErrChatHistoryUnsettled) {
			t.Fatalf("ReadHistory with a turn open = %v, want ErrChatHistoryUnsettled", err)
		}
	})
	t.Run("session id mismatch", func(t *testing.T) {
		conv, srv := startConversation(t)
		srv.respondTo(zcodeproto.MethodSessionRead, fixtureResult(t, "p8-resume.ndjson", 1))
		_, err := conv.ReadHistory(context.Background())
		if err == nil || !strings.Contains(err.Error(), p8SessionID) || !strings.Contains(err.Error(), "sess-1") {
			t.Fatalf("ReadHistory of another session's snapshot = %v, want a refusal naming both ids", err)
		}
	})
	t.Run("read failure", func(t *testing.T) {
		conv, srv := startConversation(t)
		srv.replyError(zcodeproto.MethodSessionRead, zcodeproto.CodeInternal, "boom")
		if _, err := conv.ReadHistory(context.Background()); err == nil || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("ReadHistory on a refused read = %v", err)
		}
	})
}
