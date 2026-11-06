package integration

import (
	"context"
	"net/http"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/qualitygate"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// m4CheckIntegrationPreview 删除完整仓库校验、不可变摘要或持久恢复会允许缺仓批准或丢失重启证据。
func m4CheckIntegrationPreview(t *testing.T, f *m2ffFixture, gate *qualitygate.Service, artifacts []domain.ArtifactSnapshot, fence domain.SchedulerFence, partial bool) {
	t.Helper()
	ctx := context.Background()
	svc := f.m3.quality.integrations
	ids := []string{artifacts[0].ID, artifacts[1].ID}
	w := artifacts[0].WorkflowID
	response := m4HTTP[controllers.IntegrationPlanResponse](t, f, http.MethodPost, "/workflows/"+string(w)+"/integrations/preview", controllers.QualityPreviewRequest{IdempotencyKey: "real-plan", ArtifactIDs: ids})
	plan, entries := response.Plan, response.Repositories
	if plan.Status != "clean" || len(entries) != 2 || plan.PlanDigest != plan.Digest(entries) {
		t.Fatalf("plan=%+v entries=%+v", plan, entries)
	}
	for i, a := range artifacts {
		if m2smGit(t, a.Repo.WorktreePath, "rev-parse", a.Repo.TargetRef) != entries[i].ExpectedTargetSHA+"\n" {
			t.Fatal("preview changed target ref")
		}
	}
	reopened, err := sqlite.Open(f.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	svc.Store = reopened
	replay, loaded, err := svc.Preview(ctx, plan.ID, w, ids, fence)
	if err != nil || replay != plan || len(loaded) != 2 || replay.PlanDigest != replay.Digest(loaded) {
		t.Fatalf("reloaded=%+v %v", replay, err)
	}
	if _, _, err := svc.Preview(ctx, plan.ID, w, ids[:1], fence); err == nil {
		t.Fatal("same plan ID changed repository set")
	}
	if _, _, err := svc.Preview(ctx, "partial", w, ids[:1], fence); err == nil {
		t.Fatal("new plan omitted required repository")
	}
	m4CheckIntegrationApply(t, f, svc, plan, loaded, artifacts, fence, partial)
	tampered := entries[0]
	tampered.TaskCommitSHA = entries[1].TaskCommitSHA
	entries[0] = tampered
	if _, err := reopened.CreateIntegrationPlan(ctx, plan, entries, fence, f.m3.clock.Now()); err == nil {
		t.Fatal("tampered preview accepted")
	}
}
