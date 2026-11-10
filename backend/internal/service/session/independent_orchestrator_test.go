package session

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// 独立创建、默认入口和清理替换必须互不抢占，重命名仍走现有校验。
func TestIndependentOrchestratorDoesNotReplacePrimary(t *testing.T) {
	ctx := context.Background()
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	st.sessions["primary"] = domain.SessionRecord{ID: "primary", ProjectID: "mer", Kind: domain.KindOrchestrator}
	st.sessions["independent"] = domain.SessionRecord{ID: "independent", ProjectID: "mer", Kind: domain.KindOrchestrator, IndependentOrchestrator: true}
	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}
	for range 2 {
		if _, _, _, err := svc.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindOrchestrator, IndependentOrchestrator: true}); err != nil {
			t.Fatal(err)
		}
	}
	if fc.spawnCalls != 2 || !fc.spawnedCfg.IndependentOrchestrator {
		t.Fatalf("independent creates = %d, config = %+v", fc.spawnCalls, fc.spawnedCfg)
	}
	got, err := svc.SpawnOrchestrator(ctx, "mer", false, "", "")
	if err != nil || got.ID != "primary" || fc.spawnCalls != 2 {
		t.Fatalf("primary ensure = %s, spawns = %d, err = %v", got.ID, fc.spawnCalls, err)
	}
	if err := svc.Rename(ctx, "independent", "Fix login"); err != nil || st.sessions["independent"].DisplayName != "Fix login" {
		t.Fatalf("independent rename: %v", err)
	}
	if _, err := svc.SpawnOrchestrator(ctx, "mer", true, "", ""); err != nil {
		t.Fatal(err)
	}
	if len(fc.retired) != 1 || fc.retired[0] != "primary" {
		t.Fatalf("retired = %v, independent session must survive", fc.retired)
	}
}
