package store_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// 删除事务回滚会在最后的幂等写入失败时留下 Attempt、资源或预算预留。
func TestM3AdmissionLastWriteFailureRollsBackAllFacts(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := sqlitetest.MustOpenAt(t, dir)
	now := workflowStoreTime()
	f := sqlitetest.NewAdmissionFixture(t, s, func() time.Time { return now })
	in := m3ClaimInput(t, s, f, "abort", now)
	n := int64(10)
	p := domain.BudgetPolicy{ID: "abort-budget", Scope: "global", Currency: "USD", Mode: domain.BudgetHardAdmission, Period: domain.BudgetLifetime, LimitNanos: 100, ReservationNanos: &n, ReservationBasis: domain.ReservationOperatorApproved, ReservationRef: "approval"}
	if _, _, err := s.PutBudgetPolicy(ctx, p, 0, domain.PolicyChange{IdempotencyKey: "abort-budget", RequestFingerprint: strings.Repeat("a", 64), ActorRef: "test", Reason: "TEST_BUDGET", Now: now}); err != nil {
		t.Fatal(err)
	}
	in.Evidence.ExpectedPolicyRevisions[p.RevisionKey()] = 1
	db, err := sql.Open("sqlite", filepath.Join(dir, "ao.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER m3_abort_claim BEFORE INSERT ON workflow_idempotency BEGIN SELECT RAISE(ABORT,'injected final write failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, created, err := s.ReserveAndClaim(ctx, in); err == nil || created {
		t.Fatal("fault did not reject claim")
	}
	ads, _ := s.ListSchedulerAdmissions(ctx, false)
	rs, _ := s.ListBudgetReservations(ctx, "")
	attempts, _ := s.ListWorkflowAttempts(ctx, in.Attempt.WorkflowID, in.Attempt.TaskID)
	task, _, _ := s.GetWorkflowTask(ctx, in.Attempt.WorkflowID, in.Attempt.TaskID)
	if len(ads)+len(rs)+len(attempts) != 0 || task.State != domain.WorkflowTaskStateReady {
		t.Fatalf("partial transaction: ads=%d reservations=%d attempts=%d state=%s", len(ads), len(rs), len(attempts), task.State)
	}
}

// 删除稳定请求键合并会让一个 Session 占两槽；把准备态计槽会错误拒绝第二个执行。
func TestM3SessionAdmissionIdentityAndPreparation(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "session-admission")
	now := workflowStoreTime()
	f := sqlitetest.NewAdmissionFixture(t, s, func() time.Time { return now })
	e, err := f.PrepareWorkflowAdmission(ctx, domain.WorkflowTask{})
	if err != nil {
		t.Fatal(err)
	}
	in := domain.SessionAdmissionInput{ProjectID: "session-admission", Harness: domain.HarnessCodex, Mode: domain.SessionModeChat, ClientRequestID: "session-1", ClientRequestHash: strings.Repeat("a", 64), Evidence: e}
	first, created, err := s.ReserveSessionAdmission(ctx, in)
	if err != nil || !created {
		t.Fatal(err)
	}
	r := sampleRecord("session-admission")
	r.Harness = domain.HarnessCodex
	r.ClientRequestID = in.ClientRequestID
	r.ClientRequestHash = in.ClientRequestHash
	if _, _, err := s.CreateClientRequestSession(ctx, r); err != nil {
		t.Fatal(err)
	}
	replay, created, err := s.ReserveSessionAdmission(ctx, in)
	if err != nil || created || replay.ID != first.ID {
		t.Fatalf("session replay: %v %v", created, err)
	}
	r.ClientRequestID = ""
	r.ClientRequestHash = ""
	r.IsTaskPreparation = true
	if _, err := s.CreateSession(ctx, r); err != nil {
		t.Fatal(err)
	}
	in.ClientRequestID = "session-2"
	if _, created, err := s.ReserveSessionAdmission(ctx, in); err != nil || !created {
		t.Fatalf("double counted identity/preparation: %v", err)
	}
	in.ClientRequestID = "session-3"
	if _, _, err := s.ReserveSessionAdmission(ctx, in); !errors.Is(err, domain.ErrSchedulerCapacity) {
		t.Fatalf("manual bypass: %v", err)
	}
	in.ClientRequestID = "session-1"
	in.ClientRequestHash = strings.Repeat("b", 64)
	if _, _, err := s.ReserveSessionAdmission(ctx, in); !errors.Is(err, domain.ErrSchedulerAdmissionConflict) {
		t.Fatalf("manual fingerprint conflict: %v", err)
	}
}

// 删除月窗归属或按 TTL 清占位会使旧月预留丢失；新月仅使用自己的预算窗口。
func TestM3MonthlyReservationSurvivesEpochAndMonth(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := time.Date(2026, 11, 30, 23, 59, 0, 0, time.UTC)
	f := sqlitetest.NewAdmissionFixture(t, s, func() time.Time { return now })
	n := int64(5)
	p := domain.BudgetPolicy{ID: "monthly", Scope: "global", Currency: "USD", Mode: domain.BudgetHardAdmission, Period: domain.BudgetMonthlyUTC, LimitNanos: 5, ReservationNanos: &n, ReservationBasis: domain.ReservationOperatorApproved, ReservationRef: "approved"}
	if _, _, err := s.PutBudgetPolicy(ctx, p, 0, domain.PolicyChange{IdempotencyKey: "month", RequestFingerprint: strings.Repeat("a", 64), ActorRef: "test", Reason: "TEST_BUDGET", Now: now}); err != nil {
		t.Fatal(err)
	}
	first := m3ClaimInput(t, s, f, "nov", now)
	_, ad, _, err := s.ReserveAndClaim(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	second := m3ClaimInput(t, s, f, "dec", now)
	if _, _, _, err := s.ReserveAndClaim(ctx, second); err != nil {
		t.Fatal(err)
	}
	rs, err := s.ListBudgetReservations(ctx, ad.ID)
	if err != nil || len(rs) != 1 || rs[0].Window != "2026-11" || rs[0].State != "reserved" {
		t.Fatalf("old reservation: %+v %v", rs, err)
	}
	ads, _ := s.ListSchedulerAdmissions(ctx, true)
	if len(ads) != 2 {
		t.Fatalf("TTL freed occupancy: %d", len(ads))
	}
}
