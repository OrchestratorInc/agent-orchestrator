package store_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// 删除任何预算层的余额/UNKNOWN 检查会让相应子例批准无法覆盖的预留。
func TestM3AdmissionChecksEveryBudgetLayerAndUnknown(t *testing.T) {
	for _, scope := range []string{"global", "workflow", "task"} {
		t.Run(scope, func(t *testing.T) {
			s := newTestStore(t)
			now := workflowStoreTime()
			f := sqlitetest.NewAdmissionFixture(t, s, func() time.Time { return now })
			in := m3ClaimInput(t, s, f, "budget", now)
			for _, layer := range []string{"global", "workflow", "task"} {
				scopeID := ""
				if layer == "workflow" {
					scopeID = string(in.Attempt.WorkflowID)
				}
				if layer == "task" {
					scopeID = string(in.Attempt.TaskID)
				}
				reserve := int64(5)
				limit := int64(100)
				if layer == scope {
					limit = 4
				}
				p := domain.BudgetPolicy{ID: "budget-" + layer, Scope: layer, ScopeID: scopeID, Currency: "USD", Mode: domain.BudgetHardAdmission, Period: domain.BudgetLifetime, LimitNanos: limit, ReservationNanos: &reserve, ReservationBasis: domain.ReservationOperatorApproved, ReservationRef: "approved-task"}
				if _, _, err := s.PutBudgetPolicy(context.Background(), p, 0, domain.PolicyChange{IdempotencyKey: p.ID, RequestFingerprint: strings.Repeat("a", 64), ActorRef: "sqlitetest", Reason: "TEST_BUDGET", Now: now}); err != nil {
					t.Fatal(err)
				}
				in.Evidence.ExpectedPolicyRevisions[p.RevisionKey()] = 1
			}
			if _, _, created, err := s.ReserveAndClaim(context.Background(), in); created || !errors.Is(err, domain.ErrBudgetExceeded) {
				t.Fatalf("%s: %v %v", scope, created, err)
			}
			rs, _ := s.ListBudgetReservations(context.Background(), "")
			ads, _ := s.ListSchedulerAdmissions(context.Background(), false)
			attempts, _ := s.ListWorkflowAttempts(context.Background(), in.Attempt.WorkflowID, in.Attempt.TaskID)
			if len(rs)+len(ads)+len(attempts) != 0 {
				t.Fatal("budget rejection left partial rows")
			}
		})
	}
	for _, mode := range []string{"unknown", "overflow"} {
		t.Run(mode, func(t *testing.T) {
			s := newTestStore(t)
			now := workflowStoreTime()
			f := sqlitetest.NewAdmissionFixture(t, s, func() time.Time { return now })
			in := m3ClaimInput(t, s, f, "quality", now)
			p := domain.BudgetPolicy{ID: "hard", Scope: "global", Currency: "USD", Mode: domain.BudgetHardAdmission, Period: domain.BudgetLifetime, LimitNanos: math.MaxInt64}
			if mode == "overflow" {
				n := int64(math.MaxInt64)
				p.ReservationNanos = &n
				p.ReservationBasis = domain.ReservationOperatorApproved
				p.ReservationRef = "approved"
			}
			if _, _, err := s.PutBudgetPolicy(context.Background(), p, 0, domain.PolicyChange{IdempotencyKey: "quality", RequestFingerprint: strings.Repeat("a", 64), ActorRef: "sqlitetest", Reason: "TEST_BUDGET", Now: now}); err != nil {
				t.Fatal(err)
			}
			in.Evidence.ExpectedPolicyRevisions[p.RevisionKey()] = 1
			if mode == "overflow" {
				if _, _, _, err := s.ReserveAndClaim(context.Background(), in); err != nil {
					t.Fatal(err)
				}
				in = m3ClaimInput(t, s, f, "overflow", now)
			}
			_, _, _, err := s.ReserveAndClaim(context.Background(), in)
			want := domain.ErrBudgetUnknown
			if mode == "overflow" {
				want = domain.ErrBudgetOverflow
			}
			if !errors.Is(err, want) {
				t.Fatalf("quality: %v want %v", err, want)
			}
		})
	}
}

// 删除 policy revision CAS/幂等检查会把同一批准写两次，或接受迟到修改。
func TestM3PolicyReplayAndStaleRevision(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	p := domain.DefaultSchedulerPolicy("")
	p.MaxConcurrent = 4
	c := domain.PolicyChange{IdempotencyKey: "change", RequestFingerprint: strings.Repeat("a", 64), ActorRef: "operator-ref", Reason: "CAPACITY_CHANGE", Now: now}
	first, wrote, err := s.PutSchedulerPolicy(ctx, p, 1, c)
	if err != nil || !wrote {
		t.Fatal(err)
	}
	replay, wrote, err := s.PutSchedulerPolicy(ctx, p, 1, c)
	if err != nil || wrote || replay.Revision != first.Revision {
		t.Fatalf("policy replay: %+v %v %v", replay, wrote, err)
	}
	c.IdempotencyKey = "stale"
	if _, wrote, err := s.PutSchedulerPolicy(ctx, p, 1, c); err == nil || wrote {
		t.Fatal("stale policy accepted")
	}
	p.MaxConcurrent = 11
	c.IdempotencyKey = "eleven"
	if _, _, err := s.PutSchedulerPolicy(ctx, p, 2, c); !errors.Is(err, domain.ErrSchedulerPolicy) {
		t.Fatalf("hard cap bypass: %v", err)
	}
}

// 删除预算 revision 命名空间会让同名资源/预算策略覆盖证据，导致拒绝合法请求或接受陈旧策略。
func TestM3BudgetRevisionNamespaceDoesNotCollideWithResourcePolicy(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	f := sqlitetest.NewAdmissionFixture(t, s, func() time.Time { return now })
	m3Capacity(t, s, "", 4, now)
	n := int64(1)
	p := domain.BudgetPolicy{ID: "global", Scope: "global", Currency: "USD", Mode: domain.BudgetHardAdmission, Period: domain.BudgetLifetime, LimitNanos: 100, ReservationNanos: &n, ReservationBasis: domain.ReservationOperatorApproved, ReservationRef: "approval"}
	if _, _, err := s.PutBudgetPolicy(ctx, p, 0, domain.PolicyChange{IdempotencyKey: "budget-global", RequestFingerprint: strings.Repeat("a", 64), ActorRef: "test", Reason: "TEST_BUDGET", Now: now}); err != nil {
		t.Fatal(err)
	}
	in := m3ClaimInput(t, s, f, "namespaced", now)
	if _, _, _, err := s.ReserveAndClaim(ctx, in); err != nil {
		t.Fatalf("distinct revisions collided: %v", err)
	}
	in = m3ClaimInput(t, s, f, "stale-budget", now)
	delete(in.Evidence.ExpectedPolicyRevisions, p.RevisionKey())
	if _, _, _, err := s.ReserveAndClaim(ctx, in); !errors.Is(err, domain.ErrBudgetPolicy) {
		t.Fatalf("missing budget revision: %v", err)
	}
}
