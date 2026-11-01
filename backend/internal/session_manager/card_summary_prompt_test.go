package sessionmanager

import (
	"context"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Only workers get the card summary section, and only while the setting is on
// (an unwired setting counts as off).
func TestBuildSystemPromptCardSummaryFollowsSetting(t *testing.T) {
	ctx := context.Background()
	st := newFakeStore()
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, DataDir: t.TempDir(),
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	hasSection := func(kind domain.SessionKind) bool {
		t.Helper()
		sp, err := m.buildSystemPrompt(ctx, kind, "mer", "mer-1", "", true)
		if err != nil {
			t.Fatalf("buildSystemPrompt(%s): %v", kind, err)
		}
		return strings.Contains(sp, "## Card Summary")
	}

	if hasSection(domain.KindWorker) {
		t.Fatal("worker got the card summary section without the setting")
	}
	enabled := false
	m.SetCardSummariesEnabled(func(context.Context) bool { return enabled })
	if hasSection(domain.KindWorker) {
		t.Fatal("worker got the card summary section while it is off")
	}
	enabled = true
	if !hasSection(domain.KindWorker) {
		t.Fatal("worker missing the card summary section while it is on")
	}
	if hasSection(domain.KindOrchestrator) {
		t.Fatal("orchestrator got the card summary section")
	}
}
