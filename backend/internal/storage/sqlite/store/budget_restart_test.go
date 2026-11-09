package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// 删除唯一 Binding 归因或结算 generation 条件，会重复修订同一费用或用新执行的终止事实释放旧预算。
func TestM3BudgetRestartsKeepOneEventAndFenceOldReservations(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := sqlitetest.Open(dir)
	mustNoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	now := workflowStoreTime()
	p := putLedgerPolicy(t, s, "restarts", "global", "", domain.BudgetObserve, 100, now)
	seedProject(t, s, "usage")
	seed := sampleRecord("usage")
	seed.Harness, seed.Mode, seed.IsTerminated = domain.HarnessCodex, domain.SessionModeChat, true
	seed.Metadata.ControllerGeneration = "generation-2"
	session, err := s.CreateSession(ctx, seed)
	mustNoError(t, err)
	raw, err := sql.Open("sqlite", filepath.Join(dir, "ao.db"))
	mustNoError(t, err)
	defer raw.Close()
	// 此处仅植入两次历史执行，预算用例不冒充 Manager 启动验收。
	for _, generation := range []string{"generation-1", "generation-2"} {
		_, err = raw.Exec(`INSERT INTO scheduler_admissions(id,session_id,harness,mode,client_request_id,request_fingerprint,owner_id,epoch,state,revision,created_at,updated_at,controller_generation)
		 VALUES(?,?,'codex','chat',?,?,'offline',1,'released',1,?,?,?)`, generation, session.ID, generation, strings.Repeat("a", 64), now, now, generation)
		mustNoError(t, err)
		_, err = raw.Exec(`INSERT INTO budget_reservations(admission_id,policy_id,window,amount_nanos,basis,reference,state,revision)
		 VALUES(?,?,?,10,'operator_approved','test','uncertain',1)`, generation, p.ID, p.Window(now))
		mustNoError(t, err)
	}
	source := seedUsageSource(t, s, session, now)
	event := pricedCandidateEvent("restarted-session-event")
	event.CreatedAt, event.Costs = now, ledgerCosts(30, "restart-price")
	mustNoError(t, s.ApplyUsageChunk(ctx, source.ID, 0, source.UpdatedAt, domain.SourceCursorState{ByteOffset: 10, State: domain.UsageSourceComplete, UpdatedAt: now}, []domain.ModelUsageEvent{event}))
	_, err = s.UpdateUsageBindingState(ctx, source.BindingID, domain.UsageBindingComplete, "", now)
	mustNoError(t, err)
	n, err := s.ReconcileBudgetUsage(ctx, 100, now)
	mustNoError(t, err)
	if n != 1 {
		t.Fatalf("一个事件被多个 Admission 放大为 %d 次修订", n)
	}
	entries, err := s.ListBudgetUsagePage(ctx, "global", "", 0, 100)
	mustNoError(t, err)
	if len(entries) != 1 || entries[0].Revision != 1 {
		t.Fatalf("费用被重复修订: %+v", entries)
	}
	n, err = s.ReconcileReleasedBudgetReservations(ctx, 100, now)
	mustNoError(t, err)
	if n != 1 {
		t.Fatalf("应仅结算当前终止 generation，实际=%d", n)
	}
	reservations, err := s.ListBudgetReservations(ctx, "")
	mustNoError(t, err)
	for _, reservation := range reservations {
		want := "consumed"
		if reservation.AdmissionID == "generation-1" {
			want = "uncertain"
		}
		if reservation.State != want {
			t.Fatalf("跨代际结算: %+v", reservation)
		}
	}
}
