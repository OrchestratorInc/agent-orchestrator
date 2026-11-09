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

// 删除 legacy Attempt 合并或未知 Harness 计槽会让升级后的第三个执行绕过 Adapter 上限。
func TestM3LegacyUnknownHarnessStillOccupiesSlots(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := sqlitetest.MustOpenAt(t, dir)
	now := workflowStoreTime()
	f := sqlitetest.NewAdmissionFixture(t, s, func() time.Time { return now })
	db, err := sql.Open("sqlite", filepath.Join(dir, "ao.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, id := range []string{"old-a", "old-b"} {
		in := m3ClaimInput(t, s, f, id, now)
		a := in.Attempt
		if _, err := db.Exec(`INSERT INTO workflow_attempts(id,workflow_id,task_id,attempt_no,worker_id,lease_token,lease_until,state,created_at,updated_at) VALUES(?,?,?,1,?,?,?,'unknown',?,?)`, string(a.ID), string(a.WorkflowID), string(a.TaskID), a.WorkerID, a.LeaseToken, *a.LeaseUntil, a.CreatedAt, a.UpdatedAt); err != nil {
			t.Fatal(err)
		}
	}
	in := m3ClaimInput(t, s, f, "new", now)
	if _, _, _, err := s.ReserveAndClaim(ctx, in); !errors.Is(err, domain.ErrSchedulerCapacity) {
		t.Fatalf("legacy free slot: %v", err)
	}
}

// 删除 Binding 身份合并会把同一 Attempt 和已绑定 Session 当作两个全局槽。
func TestM3BindingSessionAndAdmissionCountOnce(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	a := seedClaimedAttempt(t, s, "bound-workflow", "bound-task", "bound-project", now)
	b, repos := bindingIntent(a, "bound-project")
	b, _, err := s.CreateWorkflowAttemptBinding(sqlitetest.ExecutionContext(ctx, t, s, b.AttemptID, now.Add(3*time.Second)), b, repos, now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	rec := sampleRecord("bound-project")
	rec.Harness = domain.HarnessCodex
	session, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.BindWorkflowAttemptSession(sqlitetest.ExecutionContext(ctx, t, s, a.ID, now.Add(4*time.Second)), a.ID, session.ID, "generation", domain.WorkflowBindingStateBoundReady, b.Revision, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	m3Capacity(t, s, domain.HarnessCodex, 10, now)
	f := sqlitetest.NewAdmissionFixture(t, s, func() time.Time { return now.Add(5 * time.Second) })
	for _, id := range []string{"second", "third"} {
		in := m3ClaimInput(t, s, f, id, now.Add(5*time.Second))
		if _, _, _, err := s.ReserveAndClaim(ctx, in); err != nil {
			t.Fatalf("double-counted binding at %s: %v", id, err)
		}
	}
}

// 删除未覆盖占位检查会在执行后新设硬预算时把现有未知费用当作零。
func TestM3NewHardBudgetDoesNotForgetExistingAdmission(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	f := sqlitetest.NewAdmissionFixture(t, s, func() time.Time { return now })
	first := m3ClaimInput(t, s, f, "before-policy", now)
	if _, _, _, err := s.ReserveAndClaim(ctx, first); err != nil {
		t.Fatal(err)
	}
	n := int64(1)
	p := domain.BudgetPolicy{ID: "added-hard", Scope: "global", Currency: "USD", Mode: domain.BudgetHardAdmission, Period: domain.BudgetLifetime, LimitNanos: 100, ReservationNanos: &n, ReservationBasis: domain.ReservationOperatorApproved, ReservationRef: "approval"}
	if _, _, err := s.PutBudgetPolicy(ctx, p, 0, domain.PolicyChange{IdempotencyKey: "added-hard", RequestFingerprint: strings.Repeat("a", 64), ActorRef: "test", Reason: "TEST_BUDGET", Now: now}); err != nil {
		t.Fatal(err)
	}
	second := m3ClaimInput(t, s, f, "after-policy", now)
	if _, _, _, err := s.ReserveAndClaim(ctx, second); !errors.Is(err, domain.ErrBudgetUnknown) {
		t.Fatalf("unknown existing cost treated as zero: %v", err)
	}
}
