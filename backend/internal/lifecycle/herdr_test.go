package lifecycle

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestActivitySignalExpectedHarnessFencesStateAndMetadata(t *testing.T) {
	for _, state := range []bool{false, true} {
		store := newFakeStore()
		store.sessions["mer-1"] = domain.SessionRecord{
			ID: "mer-1", Harness: domain.HarnessCodex,
			Activity: domain.Activity{State: domain.ActivityIdle},
			Metadata: domain.SessionMetadata{RuntimeLaunchID: "launch-1", AgentSessionID: "codex-native"},
		}
		m := New(store, nil)
		if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{
			Valid: state, State: domain.ActivityActive, LaunchID: "launch-1",
			AgentSessionID: "fx-native", ExpectedHarness: domain.HarnessFX,
		}); err != nil {
			t.Fatal(err)
		}
		got := store.sessions["mer-1"]
		if got.Activity.State != domain.ActivityIdle || got.Metadata.AgentSessionID != "codex-native" {
			t.Fatalf("fx report changed another harness: %+v", got)
		}
	}
}
