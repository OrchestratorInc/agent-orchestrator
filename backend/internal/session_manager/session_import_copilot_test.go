package sessionmanager

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/copilot"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type copilotImportAgents struct{}

func (copilotImportAgents) Agent(domain.AgentHarness) (ports.Agent, bool) { return copilot.New(), true }

func TestImportedCopilotUsesTheNativeHistoryHome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("COPILOT_HOME", root)
	rec := domain.SessionRecord{Harness: domain.HarnessCopilot}
	rec.Metadata = domain.SessionMetadata{ProviderConversationID: "native", ImportSource: &domain.SessionImportSource{NativeID: "native", ConfigDir: root}}
	manager := &Manager{agents: copilotImportAgents{}}
	env := map[string]string{}
	manager.applyImportedConfigEnv(context.Background(), rec, env)
	if _, forced := env["COPILOT_HOME"]; forced {
		t.Fatal("the already-correct native home was forced")
	}
	source := filepath.Join(t.TempDir(), "other-copilot-home")
	rec.Metadata.ImportSource.ConfigDir = source
	env = map[string]string{"COPILOT_HOME": root, "GITHUB_TOKEN": "synthetic-account-token"}
	manager.applyImportedConfigEnv(context.Background(), rec, env)
	if env["COPILOT_HOME"] != source || env["GITHUB_TOKEN"] != "synthetic-account-token" {
		t.Fatal("resume did not select the source store while preserving account credentials")
	}
	relocated, err := manager.relocateImportedSession(context.Background(), rec, filepath.Join(t.TempDir(), "worktree"), nil)
	if err != nil || relocated.Metadata.ImportSource.ResumeCwd != "" {
		t.Fatalf("ID-addressed Copilot import was unnecessarily relocated: %v", err)
	}
}
