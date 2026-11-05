package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// executionFixture 使用真实准入和唯一 Session 消费事务，不运行任何 Provider。
type executionFixture struct {
	s       *sqlite.Store
	now     time.Time
	ad      domain.SchedulerAdmission
	seed    domain.SessionRecord
	attempt domain.WorkflowAttempt
}

func newExecutionFixture(t *testing.T) *executionFixture {
	t.Helper()
	x := &executionFixture{s: newTestStore(t), now: workflowStoreTime()}
	seedProject(t, x.s, "workflow-project")
	fixture := sqlitetest.NewAdmissionFixture(t, x.s, func() time.Time { return x.now })
	in := m3ClaimInput(t, x.s, fixture, "execution", x.now)
	in.Attempt.LeaseUntil = new(x.now.Add(20 * time.Second))
	amount := int64(10)
	policy := domain.BudgetPolicy{ID: "exec-budget", Scope: "global", Currency: "USD", Mode: domain.BudgetHardAdmission, Period: domain.BudgetLifetime, LimitNanos: 100, ReservationNanos: &amount, ReservationBasis: domain.ReservationOperatorApproved, ReservationRef: "test"}
	if _, _, err := x.s.PutBudgetPolicy(context.Background(), policy, 0, domain.PolicyChange{IdempotencyKey: "exec-policy", RequestFingerprint: strings.Repeat("d", 64), ActorRef: "test", Reason: "TEST_BUDGET", Now: x.now}); err != nil {
		t.Fatal(err)
	}
	in.Evidence.ExpectedPolicyRevisions[policy.RevisionKey()] = 1
	var err error
	x.attempt, x.ad, _, err = x.s.ReserveAndClaim(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	binding := domain.WorkflowAttemptBinding{AttemptID: x.attempt.ID, WorkflowID: x.attempt.WorkflowID, TaskID: x.attempt.TaskID, SpawnRequestID: "execution-spawn", SpawnHash: strings.Repeat("e", 64)}
	binding, _, err = x.s.CreateWorkflowAttemptBinding(x.context(t), binding, nil, x.now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = x.s.TransitionWorkflowAttemptBinding(x.context(t), binding.AttemptID, domain.WorkflowBindingStateSpawnRequested, "", binding.Revision, x.now); err != nil {
		t.Fatal(err)
	}
	x.seed = domain.SessionRecord{ProjectID: "workflow-project", Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeChat, ClientRequestID: binding.SpawnRequestID, ClientRequestHash: binding.SpawnHash, Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: x.now}, CreatedAt: x.now, UpdatedAt: x.now, Metadata: domain.SessionMetadata{ControllerGeneration: "generation-1"}}
	return x
}

func (x *executionFixture) context(t *testing.T) context.Context {
	t.Helper()
	p, err := x.s.LoadExecutionPermit(context.Background(), x.attempt.ID, x.ad.Fence, x.now)
	if err != nil {
		t.Fatal(err)
	}
	return ports.WithExecutionPermit(context.Background(), p)
}

func (x *executionFixture) accept(t *testing.T) domain.SessionRecord {
	t.Helper()
	rec, fresh, err := x.s.CreateAdmittedSession(x.context(t), x.seed, x.ad, "", x.now)
	if err != nil || !fresh {
		t.Fatalf("accept: fresh=%v err=%v", fresh, err)
	}
	x.ad, _, err = x.s.GetSchedulerAdmission(context.Background(), x.ad.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := x.s.GetWorkflowAttemptBinding(context.Background(), x.attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = x.s.BindWorkflowAttemptSession(x.context(t), x.attempt.ID, rec.ID, "generation-1", domain.WorkflowBindingStateBoundReady, b.Revision, x.now); err != nil {
		t.Fatal(err)
	}
	return rec
}

func (x *executionFixture) observation(rec domain.SessionRecord) ports.SessionObservation {
	return ports.SessionObservation{SessionID: rec.ID, Readiness: ports.SessionReadinessReady, ControllerGeneration: "generation-1", ClientRequestID: rec.ClientRequestID, ObservedAt: x.now}
}
