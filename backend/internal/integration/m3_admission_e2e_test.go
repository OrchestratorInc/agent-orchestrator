package integration

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestM3RealManagerSharedCapacity 删除原子 Admission 容量检查或 Manager 消费占位检查会创建第四/第十一个 Session。
func TestM3RealManagerSharedCapacity(t *testing.T) {
	f := newM3Fixture(t)
	wid := m3Create(t, f, "capacity", 11)
	for i := 0; i < 3; i++ {
		m3Dispatch(t, f)
	}
	m3AssertOccupancy(t, f, 2)
	m3Policy(t, f, domain.HarnessCodex, 10)
	for i := 0; i < 4; i++ {
		m3Dispatch(t, f)
	}
	m3AssertOccupancy(t, f, 3)
	m3Policy(t, f, "", 10)
	for i := 0; i < 8; i++ {
		m3Dispatch(t, f)
	}
	m3AssertOccupancy(t, f, 10)
	tasks, err := f.store.ListWorkflowTasks(context.Background(), wid, domain.WorkflowTaskStateReady, 100, 0)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("queued eleventh: tasks=%d err=%v", len(tasks), err)
	}
	res := f.asOperator(http.MethodPost, "/workflows/"+string(wid)+"/tasks/"+string(tasks[0].ID)+"/claim",
		fmt.Sprintf(`{"expectedRevision":%d,"workerId":"manual","idempotencyKey":"eleventh"}`, tasks[0].Revision))
	if res.Status != http.StatusConflict || f.text(res.Body, "code") != "WORKFLOW_ADMISSION_DENIED" {
		t.Fatalf("eleventh: %d %s", res.Status, res.Raw)
	}
	m3AssertOccupancy(t, f, 10)
}

// m3AssertOccupancy 同时比较物理 Session、持久 Admission 和控制面状态，避免只验证返回值。
func m3AssertOccupancy(t *testing.T, f *m2ffFixture, want int) {
	t.Helper()
	ads, err := f.store.ListSchedulerAdmissions(context.Background(), true)
	if err != nil || len(ads) != want {
		t.Fatalf("admissions=%d want=%d err=%v", len(ads), want, err)
	}
	if n := len(f.sessionsFor()); n != want {
		t.Fatalf("sessions=%d want=%d", n, want)
	}
	for _, ad := range ads {
		if ad.SessionID == "" {
			t.Fatalf("admission has no accepted session: %s", ad.ID)
		}
		rows := f.worktrees(ad.SessionID)
		if len(rows) != 1 {
			t.Fatalf("worktrees=%d", len(rows))
		}
		for _, row := range rows {
			if !worktreeListedIn(t, f.repoA, row.WorktreePath) {
				t.Fatalf("unregistered worktree %s", row.WorktreePath)
			}
		}
		if !f.chat.HasLiveChatController(ad.SessionID) {
			t.Fatalf("session controller missing: %s", ad.SessionID)
		}
	}
	r := f.asOperator(http.MethodGet, "/scheduler/status", "")
	if r.Status != http.StatusOK || f.number(r.Body, "occupied") != int64(want) {
		t.Fatalf("status: %d %s", r.Status, r.Raw)
	}
}
