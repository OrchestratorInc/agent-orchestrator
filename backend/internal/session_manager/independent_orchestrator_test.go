package sessionmanager

import (
	"errors"
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
	if st.sessions[worker.ID].SpawnParentSessionID != first.ID {
		t.Fatalf("worker creator = %s", st.sessions[worker.ID].SpawnParentSessionID)
	}
	child, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, ParentSessionID: worker.ID})
	if err != nil {
		t.Fatal(err)
	}
	if child.OrchestratorSessionID != first.ID {
		t.Fatalf("nested worker owner = %s", child.OrchestratorSessionID)
	}
	if st.sessions[child.ID].SpawnParentSessionID != worker.ID {
		t.Fatalf("nested worker creator = %s", st.sessions[child.ID].SpawnParentSessionID)
	}
	if _, found, err := m.activeOrchestratorSessionID(ctx, "mer"); err != nil || found {
		t.Fatalf("independent session became project primary: %v %v", found, err)
	}
}

// TestSpawnParentSessionIDPreservesCreator 验证真实 TUI 创建链保留直接父级，删除 seedRecord 的父级装配即失败。
func TestSpawnParentSessionIDPreservesCreator(t *testing.T) {
	m, st, _, _ := newManager()
	spawn := func(kind domain.SessionKind, parent domain.SessionID) domain.SessionRecord {
		t.Helper()
		rec, _, _, err := m.Spawn(ctx, ports.SpawnConfig{
			ProjectID: "mer", Kind: kind, ParentSessionID: parent, RequestedMode: domain.SessionModeTUI,
		})
		if err != nil {
			t.Fatal(err)
		}
		stored, found, err := st.GetSession(ctx, rec.ID)
		if err != nil || !found || stored.SpawnParentSessionID != parent || rec.SpawnParentSessionID != parent {
			t.Fatalf("spawn %s parent: returned=%q stored=%q want=%q found=%v err=%v", rec.ID, rec.SpawnParentSessionID, stored.SpawnParentSessionID, parent, found, err)
		}
		if stored.OrchestratorSessionID != "" {
			t.Fatalf("primary reporting binding must stay empty: %q", stored.OrchestratorSessionID)
		}
		if _, err := st.ConversationForSession(ctx, rec.ID); !errors.Is(err, domain.ErrNoConversation) {
			t.Fatalf("TUI session unexpectedly has a conversation: %v", err)
		}
		return rec
	}
	main := spawn(domain.KindOrchestrator, "")
	worker := spawn(domain.KindWorker, main.ID)
	spawn(domain.KindWorker, worker.ID)
	manual := spawn(domain.KindWorker, "")
	spawn(domain.KindWorker, manual.ID)
}
