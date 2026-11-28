package agent

import (
	"testing"

	agentregistry "github.com/OrchestratorInc/agent-orchestrator/backend/internal/adapters/agent/registry"
)

func TestReadinessAdvertisesDeclaredTurnInterrupts(t *testing.T) {
	coordinator := newReadinessCoordinator(readinessCoordinatorConfig{Agents: agentregistry.Harnessed()})
	for _, snapshot := range coordinator.Snapshot() {
		want := snapshot.ID == "claude-code" || snapshot.ID == "codex" || snapshot.ID == "gemini"
		if snapshot.CanStopTurn != want {
			t.Errorf("%s canStopTurn = %v, want %v", snapshot.ID, snapshot.CanStopTurn, want)
		}
	}
}
