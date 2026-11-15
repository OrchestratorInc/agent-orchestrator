package lifecycle

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type turnRecordingStore struct {
	*fakeStore
	calls []string
}

func (s *turnRecordingStore) RecordTerminalTurnRequest(_ context.Context, turn domain.TerminalTurn) error {
	s.calls = append(s.calls, "request:"+string(turn.Harness)+":"+turn.NativeSessionID+":"+turn.UserRequest)
	return nil
}

func (s *turnRecordingStore) RecordTerminalTurnResponse(_ context.Context, _ domain.SessionID, response string, _ time.Time) error {
	s.calls = append(s.calls, "response:"+response)
	return nil
}

func TestTerminalTurnsAreRecordedFromHooksInTerminalModeOnly(t *testing.T) {
	signals := []ports.ActivitySignal{
		{Event: "user-prompt-submit", LatestUserPrompt: "fix the importer", TurnUserRequest: "fix the importer"},
		{Event: "stop", FinalAssistantMessage: "importer fixed"},
		{Event: "user-prompt-submit", ConversationCheckpointOrigin: domain.ConversationCheckpointOriginCoordination},
		{Event: "pre-tool-use", LatestUserPrompt: "not a main-turn boundary", TurnUserRequest: "nor is this"},
		{Event: "user-prompt-submit", TurnUserRequest: "add a retry"},
		{Event: "stop", SubagentID: "helper-1", FinalAssistantMessage: "helper done"},
		{Event: "stop"},
		{Event: "user-prompt-submit", TurnUserRequest: "now add a test"},
		{Event: "user-prompt-submit"},
	}
	for _, tc := range []struct {
		name string
		rec  domain.SessionRecord
		want []string
	}{
		{
			name: "terminal",
			rec: domain.SessionRecord{ID: "s-1", Harness: domain.HarnessCodex, Mode: domain.SessionModeTUI,
				Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch", AgentSessionID: "thread-1", AgentSessionIDLaunchID: "launch"}},
			want: []string{
				"request:codex:thread-1:fix the importer", "response:importer fixed", "response:",
				"request:codex:thread-1:add a retry", "response:",
				"request:codex:thread-1:now add a test", "response:",
			},
		},
		{
			name: "chat",
			rec: domain.SessionRecord{ID: "s-1", Harness: domain.HarnessCodex, Mode: domain.SessionModeChat,
				Metadata: domain.SessionMetadata{ControllerGeneration: "generation"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &turnRecordingStore{fakeStore: newFakeStore()}
			store.sessions["s-1"] = tc.rec
			manager := New(store, nil)
			for _, signal := range signals {
				signal.Valid, signal.State = true, domain.ActivityActive
				if tc.rec.Mode == domain.SessionModeChat {
					signal.ControllerGeneration = "generation"
				} else {
					signal.LaunchID = "launch"
				}
				if err := manager.ApplyActivitySignal(context.Background(), "s-1", signal); err != nil {
					t.Fatal(err)
				}
			}
			if !reflect.DeepEqual(store.calls, tc.want) {
				t.Fatalf("recorded %q, want %q", store.calls, tc.want)
			}
		})
	}
}

func TestHookSignalFromAPreviousAgentIsIgnored(t *testing.T) {
	store := &turnRecordingStore{fakeStore: newFakeStore()}
	store.sessions["s-1"] = domain.SessionRecord{ID: "s-1", Harness: domain.HarnessCodex, Mode: domain.SessionModeTUI,
		Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch", AgentSessionID: "thread-1", AgentSessionIDLaunchID: "launch"}}
	manager := New(store, nil)
	for _, harness := range []domain.AgentHarness{domain.HarnessClaudeCode, domain.HarnessCodex} {
		if err := manager.ApplyActivitySignal(context.Background(), "s-1", ports.ActivitySignal{
			Event: "user-prompt-submit", TurnUserRequest: "ask from " + string(harness), ExpectedHarness: harness,
			LaunchID: "launch", Valid: true, State: domain.ActivityActive,
		}); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"request:codex:thread-1:ask from codex"}
	if !reflect.DeepEqual(store.calls, want) {
		t.Fatalf("recorded %q, want only the running agent's turn %q", store.calls, want)
	}
}
