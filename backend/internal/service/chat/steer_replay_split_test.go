package chat

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// steerActivity is the row Steer records on the running turn.
func steerActivity(turnID, clientID, text string) domain.ConversationActivity {
	return domain.ConversationActivity{
		TurnID: turnID, Kind: domain.ActivityKindSystem, Status: domain.ActivityStatusCompleted,
		ProviderItemID: "steer:" + clientID, Summary: text,
		Detail: []byte(`{"clientMessageId":"` + clientID + `","event":"steer","origin":"human",` +
			`"providerInputCaptured":true,"providerInputStoredOnTurn":false,` +
			`"providerInputText":"` + text + `","text":"` + text + `"}`),
	}
}

// historyTurn is one turn as ACP session/load replays it: a new turn per user
// message, settled as recovered because the replay carries no outcome.
func historyTurn(id, userText string, provider ...ports.ChatEvent) []ports.ChatEvent {
	events := []ports.ChatEvent{
		{Kind: ports.ChatEventTurnStarted, ProviderTurnID: id},
		{Kind: ports.ChatEventUserMessageCompleted, ProviderTurnID: id,
			ProviderItemID: "native-" + id, ClientMessageID: "native-" + id, Text: userText},
	}
	for _, event := range provider {
		event.ProviderTurnID = id
		events = append(events, event)
	}
	return append(events, ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderTurnID: id, TurnState: domain.TurnStateRecovered,
	})
}

func assistant(itemID, text string) ports.ChatEvent {
	return ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderItemID: itemID, Text: text}
}

// Claude stores each steer as its own user message, and ACP replay starts a new
// turn at every user message. One AO turn with two steers comes back as several
// turns, and only some of them hold an assistant item AO can match. None of them
// may come back as a new recovered turn.
func TestReplayAttachesSplitTurnsToTheirAOTurn(t *testing.T) {
	turns := []domain.ConversationTurn{
		{ID: "prompt", ProviderTurnID: "live-turn", State: domain.TurnStateCompleted},
		{ID: "steer-1", State: domain.TurnStateCompleted},
		{ID: "steer-2", State: domain.TurnStateCompleted},
	}
	activities := []domain.ConversationActivity{
		steerActivity("prompt", "client-steer-1", "some more observation"),
		steerActivity("prompt", "client-steer-2", "tell me is this true?"),
	}
	cases := []struct {
		name     string
		messages []domain.ConversationMessage
		replay   [][]ports.ChatEvent
	}{{
		// Session -349: both steers arrived after a tool call, 0.5 s apart.
		name: "steers after tool work",
		messages: []domain.ConversationMessage{
			{TurnID: "prompt", Role: domain.MessageRoleAssistant, Text: "Read-only check.", ProviderItemID: "msg-1"},
			{TurnID: "prompt", Role: domain.MessageRoleAssistant, Text: "Mostly true.", ProviderItemID: "msg-2"},
		},
		replay: [][]ports.ChatEvent{
			historyTurn("history-a", "check the claim", assistant("msg-1", "Read-only check.")),
			historyTurn("history-b", "some more observation"),
			historyTurn("history-c", "tell me is this true?", assistant("msg-2", "Mostly true.")),
		},
	}, {
		// Lab: both steers arrived before Claude's first output, so the prompt
		// is replayed alone too and the only matchable turn comes last.
		name: "steers before any output",
		messages: []domain.ConversationMessage{
			{TurnID: "prompt", Role: domain.MessageRoleAssistant, Text: "59269", ProviderItemID: "msg-2"},
		},
		replay: [][]ports.ChatEvent{
			historyTurn("history-a", "check the claim"),
			historyTurn("history-b", "some more observation"),
			historyTurn("history-c", "tell me is this true?", assistant("msg-2", "59269")),
		},
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			messages := append([]domain.ConversationMessage{
				{TurnID: "prompt", Role: domain.MessageRoleUser, Text: "check the claim", ClientMessageID: "client-prompt"},
				{TurnID: "steer-1", Role: domain.MessageRoleUser, Text: "some more observation", ClientMessageID: "client-steer-1"},
				{TurnID: "steer-2", Role: domain.MessageRoleUser, Text: "tell me is this true?", ClientMessageID: "client-steer-2"},
			}, tc.messages...)
			var events []ports.ChatEvent
			for _, turn := range tc.replay {
				events = append(events, turn...)
			}
			for _, event := range reconcileNativeHistory(events, turns, messages, activities) {
				if event.ProviderTurnID != "live-turn" {
					t.Errorf("replay event %s stayed on %q; it would be imported as a new turn", event.Kind, event.ProviderTurnID)
				}
				if event.Kind == ports.ChatEventUserMessageCompleted || event.Kind == ports.ChatEventMessageCompleted {
					t.Errorf("replay re-imported %q", event.Text)
				}
				if event.Kind == ports.ChatEventTurnCompleted && event.TurnState != domain.TurnStateCompleted {
					t.Errorf("turn state = %q, want completed", event.TurnState)
				}
			}
		})
	}
}

// Only prompts and steers AO recorded are absorbed, once each. A later message
// with the same text that AO never stored, for example one typed in the
// terminal, stays a new turn.
func TestReplayKeepsUnrecordedRepeatOfSteerText(t *testing.T) {
	turns := []domain.ConversationTurn{
		{ID: "prompt", ProviderTurnID: "live-turn", State: domain.TurnStateCompleted},
	}
	messages := []domain.ConversationMessage{
		{TurnID: "prompt", Role: domain.MessageRoleUser, Text: "start", ClientMessageID: "client-prompt"},
		{TurnID: "prompt", Role: domain.MessageRoleAssistant, Text: "done", ProviderItemID: "msg-1"},
	}
	activities := []domain.ConversationActivity{steerActivity("prompt", "client-steer", "keep going")}
	var events []ports.ChatEvent
	events = append(events, historyTurn("history-a", "start")...)
	events = append(events, historyTurn("history-b", "keep going", assistant("msg-1", "done"))...)
	events = append(events, historyTurn("history-c", "keep going")...)
	events = append(events, historyTurn("history-d", "start")...)

	kept := map[string]int{}
	for _, event := range reconcileNativeHistory(events, turns, messages, activities) {
		if event.Kind == ports.ChatEventUserMessageCompleted {
			kept[event.ProviderTurnID+" "+event.Text]++
		}
	}
	want := map[string]int{"history-c keep going": 1, "history-d start": 1}
	if len(kept) != len(want) || kept["history-c keep going"] != 1 || kept["history-d start"] != 1 {
		t.Fatalf("kept user messages = %v, want %v", kept, want)
	}
}
