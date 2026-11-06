package integration

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// TestM4ArtifactThroughRealManager 删除服务绑定检查、持久幂等或新版本失效会破坏此真实装配链。
func TestM4ArtifactThroughRealManager(t *testing.T) {
	m4ArtifactThroughRealManager(t, false)
}

// TestM4IntegrationPartialRestart 检查真实两仓执行中第二仓失败及丢失回执，重启只能对账。
func TestM4IntegrationPartialRestart(t *testing.T) {
	m4ArtifactThroughRealManager(t, true)
}

// m4ArtifactThroughRealManager 共享真实两仓装配；partial 只改变最终集成故障注入点。
func m4ArtifactThroughRealManager(t *testing.T, partial bool) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	f := newM4Fixture(t)
	body := m3JSON(t, map[string]any{"title": "M4 artifact", "goal": "offline delivery", "idempotencyKey": "m4-artifact",
		"repositories":     []map[string]any{{"repoKey": "repo-a", "projectId": f.projectA, "targetRef": "refs/heads/main"}, {"repoKey": "repo-b", "projectId": f.projectB, "targetRef": "main"}},
		"tasks":            []map[string]any{{"id": "m4-task", "kind": "development", "title": "artifact", "agentHarness": domain.HarnessCodex, "maxAttempts": 3}},
		"taskRepositories": []map[string]any{{"taskId": "m4-task", "repoKey": "repo-a", "allowedPaths": []string{"delivery.txt"}}, {"taskId": "m4-task", "repoKey": "repo-b", "allowedPaths": []string{"delivery.txt"}}}})
	r := f.asOperator(http.MethodPost, "/workflows", body)
	if r.Status != http.StatusCreated {
		t.Fatalf("create: %d %s", r.Status, r.Raw)
	}
	wid := domain.WorkflowID(f.text(r.Body, "workflow", "id"))
	r = f.asOperator(http.MethodPost, "/workflows/"+string(wid)+"/activate", fmt.Sprintf(`{"expectedRevision":%d,"idempotencyKey":"m4-activate"}`, f.number(r.Body, "workflow", "revision")))
	if r.Status != http.StatusOK {
		t.Fatalf("activate: %d %s", r.Status, r.Raw)
	}
	dispatches := m3Dispatch(t, f)
	if len(dispatches) != 1 {
		t.Fatalf("dispatches=%d", len(dispatches))
	}
	id := dispatches[0].AttemptID
	binding := f.binding(id)
	if binding.SessionID == nil {
		t.Fatal("no accepted session")
	}
	worktrees := f.worktrees(*binding.SessionID)
	worktree := worktrees["repo-a"]
	for key, w := range worktrees {
		if err := os.WriteFile(filepath.Join(w.WorktreePath, "delivery.txt"), []byte("revision 1 "+key), 0600); err != nil {
			t.Fatal(err)
		}
	}
	leader, err := f.store.GetSchedulerLeader(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	permit, err := f.store.LoadExecutionPermit(context.Background(), id, leader.Fence, f.m3.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	ctx := ports.WithExecutionPermit(context.Background(), permit)
	svc := *f.m3.quality.artifacts
	first, err := svc.Snapshot(ctx, id, 1, leader.Fence)
	if err != nil || len(first) != 2 {
		t.Fatalf("snapshot: %+v %v", first, err)
	}
	replay, err := svc.Snapshot(ctx, id, 1, leader.Fence)
	if err != nil || len(replay) != 2 || replay[0].ID != first[0].ID || replay[1].ID != first[1].ID {
		t.Fatalf("snapshot replay: %+v %v", replay, err)
	}
	m4RequestRepair(t, f, first[0])
	for _, w := range worktrees {
		if err := os.WriteFile(filepath.Join(w.WorktreePath, "delivery.txt"), []byte("revision 2"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.Revalidate(ctx, first[0]); err == nil {
		t.Fatal("changed bytes accepted old fingerprint")
	}
	snapshot := m4CLI[controllers.QualityArtifactsResponse](t, f, "snapshot", wid, string(id), controllers.QualitySnapshotRequest{ArtifactRevision: 2})
	second := snapshot.Artifacts
	if len(second) != 2 || second[0].Fingerprint == first[0].Fingerprint {
		t.Fatalf("next artifact: %+v", second)
	}
	stale, ok, err := f.store.GetArtifact(context.Background(), first[0].ID)
	if err != nil || !ok || stale.State != "stale" {
		t.Fatalf("old artifact not stale: %+v %v", stale, err)
	}
	if got := m2smGit(t, worktree.WorktreePath, "status", "--porcelain"); got != "?? delivery.txt\n" {
		t.Fatalf("worktree modified: %q", got)
	}
	// 独立连接重新载入两仓证据，不能依赖服务进程内的路径或指纹缓存。
	reopened, err := sqlite.Open(f.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for i, a := range second {
		loaded, found, err := reopened.GetArtifact(context.Background(), a.ID)
		if err != nil || !found || loaded.Digest() != a.Fingerprint || loaded.Generation != binding.ControllerGeneration {
			t.Fatalf("persisted repository identity: %+v %v", loaded, err)
		}
		if err := svc.Revalidate(ctx, loaded); err != nil {
			t.Fatalf("reloaded artifact: %v", err)
		}
		if a.Generation != "" || loaded.Generation == "" {
			t.Fatal("HTTP leaked or storage lost generation")
		}
		second[i] = loaded
	}
	m4CheckQuality(t, f, &svc, second[0], leader.Fence)
	if err := f.store.ValidateIntegrationArtifacts(ctx, wid, []string{second[0].ID}, leader.Fence, f.m3.clock.Now()); err == nil {
		t.Fatal("partial repository set accepted")
	}
	gate := m4CheckQuality(t, f, &svc, second[1], leader.Fence)
	m4CheckIntegrationPreview(t, f, gate, second, leader.Fence, partial)
}

// TestM4ReservationConflictQueuesAtScheduler 删除路径拒绝分类会让一条争用中断整轮调度而非保持排队。
func TestM4ReservationConflictQueuesAtScheduler(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	f := newM3Fixture(t)
	body := m3JSON(t, map[string]any{"title": "M4 conflict", "goal": "serialize overlapping writers", "idempotencyKey": "m4-conflict",
		"repositories":     []map[string]any{{"repoKey": "repo-a", "projectId": f.projectA}},
		"tasks":            []map[string]any{{"id": "writer-a", "kind": "development", "title": "first", "agentHarness": domain.HarnessCodex}, {"id": "writer-b", "kind": "development", "title": "second", "agentHarness": domain.HarnessCodex}},
		"taskRepositories": []map[string]any{{"taskId": "writer-a", "repoKey": "repo-a", "allowedPaths": []string{"src"}}, {"taskId": "writer-b", "repoKey": "repo-a", "allowedPaths": []string{"src/child"}}}})
	r := f.asOperator(http.MethodPost, "/workflows", body)
	if r.Status != http.StatusCreated {
		t.Fatalf("create: %d %s", r.Status, r.Raw)
	}
	wid := domain.WorkflowID(f.text(r.Body, "workflow", "id"))
	r = f.asOperator(http.MethodPost, "/workflows/"+string(wid)+"/activate", fmt.Sprintf(`{"expectedRevision":%d,"idempotencyKey":"m4-conflict-activate"}`, f.number(r.Body, "workflow", "revision")))
	if r.Status != http.StatusOK {
		t.Fatalf("activate: %d %s", r.Status, r.Raw)
	}
	if ds := m3Dispatch(t, f); len(ds) != 1 {
		t.Fatalf("overlapping dispatch count=%d", len(ds))
	}
	if ds := m3Dispatch(t, f); len(ds) != 0 {
		t.Fatal("second writer was dispatched")
	}
	queued, err := f.store.ListWorkflowTasks(context.Background(), wid, domain.WorkflowTaskStateReady, 100, 0)
	if err != nil || len(queued) != 1 {
		t.Fatalf("queued=%d err=%v", len(queued), err)
	}
	decisions, err := f.store.ListSchedulerDecisions(context.Background(), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range decisions {
		found = found || d.ReasonCode == "PATH_RESERVATION_CONFLICT"
	}
	if !found {
		t.Fatal("missing durable path conflict decision")
	}
	r = f.asOperator(http.MethodPost, "/workflows/"+string(wid)+"/tasks/"+string(queued[0].ID)+"/claim", fmt.Sprintf(`{"expectedRevision":%d,"workerId":"operator","idempotencyKey":"conflicting-manual"}`, queued[0].Revision))
	if r.Status != http.StatusConflict || f.text(r.Body, "code") != "WORKFLOW_ADMISSION_DENIED" {
		t.Fatalf("manual conflict: %d %s", r.Status, r.Raw)
	}
}
