package sessionmanager

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestDefaultChatOMPSpawnPreservesWorkerEffort covers mode resolution before tuning.
func TestDefaultChatOMPSpawnPreservesWorkerEffort(t *testing.T) {
	launcher := &recordingLauncher{}
	mgr, store, _ := newChatManager(launcher)
	mgr.defaults = fixedSessionModeDefaults(domain.SessionModeChat)
	project := store.projects[string(chatTestProject)]
	project.Config.Worker.Harness = domain.HarnessOMP
	project.Config.Worker.AgentConfig.Effort = "max"
	store.projects[string(chatTestProject)] = project

	rec, _, _, err := mgr.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: chatTestProject, Kind: domain.KindWorker, Harness: domain.HarnessOMP,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if rec.Mode != domain.SessionModeChat || len(launcher.started) != 1 {
		t.Fatalf("mode/start count = %q/%d, want chat/1", rec.Mode, len(launcher.started))
	}
	if rec.Metadata.Effort != "max" || launcher.started[0].Effort != "max" {
		t.Fatalf("effort metadata/start = %q/%q, want max/max", rec.Metadata.Effort, launcher.started[0].Effort)
	}
}
