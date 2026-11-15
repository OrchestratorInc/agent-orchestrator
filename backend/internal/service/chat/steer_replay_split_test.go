package chat

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func steerActivity(turnID, clientID, text string) domain.ConversationActivity {
	return domain.ConversationActivity{
		TurnID: turnID, Kind: domain.ActivityKindSystem, Status: domain.ActivityStatusCompleted,
		ProviderItemID: "steer:" + clientID, Summary: text,
		Detail: []byte(`{"clientMessageId":"` + clientID + `","event":"steer","origin":"human",` +
			`"providerInputCaptured":true,"providerInputStoredOnTurn":false,` +
			`"providerInputText":"` + text + `","text":"` + text + `"}`),
	}
}

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
