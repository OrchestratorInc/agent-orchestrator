package sessionmanager

import (
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// 通过真实 Spawn 装配检查分支隔离及恢复后仍可用的派发归属。
func TestIndependentOrchestratorSpawnAndWorkerOwnership(t *testing.T) {
	m, st, _, _ := newManager()
	first, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindOrchestrator, IndependentOrchestrator: true})
	if err != nil {
		t.Fatal(err)
	}
	second, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindOrchestrator, IndependentOrchestrator: true})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID || !first.IndependentOrchestrator || first.Metadata.Branch == second.Metadata.Branch || !strings.Contains(first.Metadata.Branch, string(first.ID)) {
		t.Fatalf("independent sessions do not own unique branches: %+v / %+v", first, second)
	}
	worker, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, ParentSessionID: first.ID})
	if err != nil {
		t.Fatal(err)
	}
	if st.sessions[worker.ID].OrchestratorSessionID != first.ID {
		t.Fatalf("worker owner = %s", st.sessions[worker.ID].OrchestratorSessionID)
	}
	child, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, ParentSessionID: worker.ID})
	if err != nil {
		t.Fatal(err)
	}
	if child.OrchestratorSessionID != first.ID {
		t.Fatalf("nested worker owner = %s", child.OrchestratorSessionID)
	}
	if _, found, err := m.activeOrchestratorSessionID(ctx, "mer"); err != nil || found {
		t.Fatalf("independent session became project primary: %v %v", found, err)
	}
}
