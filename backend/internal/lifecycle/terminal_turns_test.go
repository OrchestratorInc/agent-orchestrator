package lifecycle

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// turnRecordingStore captures what lifecycle writes to the terminal turn table.
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
		// Claude and Codex report the request as a replay checkpoint too.
		{Event: "user-prompt-submit", LatestUserPrompt: "fix the importer", TurnUserRequest: "fix the importer"},
		// Codex reports its final message without making it a replay checkpoint.
		{Event: "stop", FinalAssistantMessage: "importer fixed"},
		// AO's own message opens a turn the user did not ask for.
		{Event: "user-prompt-submit", ConversationCheckpointOrigin: domain.ConversationCheckpointOriginCoordination},
		{Event: "pre-tool-use", LatestUserPrompt: "not a main-turn boundary", TurnUserRequest: "nor is this"},
		// OpenCode and Grok report the request outside the replay-checkpoint field.
		{Event: "user-prompt-submit", TurnUserRequest: "add a retry"},
		// A helper agent finishing is not the end of the turn.
		{Event: "stop", SubagentID: "helper-1", FinalAssistantMessage: "helper done"},
		// Some agents report the end of a turn without its text. The turn
		// still closes, so the next request starts one of its own.
		{Event: "stop"},
		{Event: "user-prompt-submit", TurnUserRequest: "now add a test"},
		// A prompt hook with no text is not a request; it closes the open turn.
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

// After a provider switch the previous agent's hook config can still be in the
// workspace. A signal that names an agent the session no longer runs is dropped
// before it can record a turn or change activity.
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
