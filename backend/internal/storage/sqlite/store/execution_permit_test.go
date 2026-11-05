package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// 删除消费事务的 epoch 校验会接受旧任期；删除 Session 重放分支会重复插入或错误报告 fresh。
func TestM3ExecutionAcceptanceFencesEpochAndKeepsStableSession(t *testing.T) {
	x := newExecutionFixture(t)
	oldCtx := x.context(t)
	x.now = x.now.Add(31 * time.Second)
	leader, _, err := x.s.AcquireSchedulerLeader(context.Background(), "new-owner", x.now, x.now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := x.s.CreateAdmittedSession(oldCtx, x.seed, x.ad, "", x.now); !errors.Is(err, domain.ErrSchedulerFence) {
		t.Fatalf("old epoch accepted: %v", err)
	}
	sessions, err := x.s.ListAllSessions(context.Background())
	if err != nil || len(sessions) != 0 {
		t.Fatalf("partial session: %d %v", len(sessions), err)
	}
	if _, _, err := x.s.AdoptSchedulerAdmission(context.Background(), x.ad.ID, x.ad.Revision, leader.Fence, x.now); err != nil {
		t.Fatal(err)
	}
	ad, _, _ := x.s.GetSchedulerAdmission(context.Background(), x.ad.ID)
	if ad.State != domain.AdmissionUncertain {
		t.Fatal("takeover dropped occupied slot")
	}

	y := newExecutionFixture(t)
	rec := y.accept(t)
	replay, fresh, err := y.s.CreateAdmittedSession(y.context(t), y.seed, y.ad, "", y.now)
	if err != nil || fresh || replay.ID != rec.ID {
		t.Fatalf("replay: %s fresh=%v err=%v", replay.ID, fresh, err)
	}
	lateCtx := y.context(t)
	y.now = y.now.Add(31 * time.Second)
	if _, _, err := y.s.AcquireSchedulerLeader(context.Background(), "replacement", y.now, y.now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	b, _, _ := y.s.GetWorkflowAttemptBinding(context.Background(), y.attempt.ID)
	if _, _, err := y.s.ReanchorWorkflowAttemptGeneration(lateCtx, y.attempt.ID, rec.ID, "late-generation", b.Revision, b.State, y.now); !errors.Is(err, domain.ErrSchedulerFence) {
		t.Fatalf("late receipt wrote: %v", err)
	}
}

// 删除 worker/token/revision/代际检查会让旧快照续租；删除到期检查会复活已过期 Attempt。
func TestM3ExecutionLeaseRequiresFreshLiveEvidence(t *testing.T) {
	x := newExecutionFixture(t)
	rec := x.accept(t)
	p, _ := ports.ExecutionPermitFromContext(x.context(t))
	ob := x.observation(rec)
	stale := p
	stale.WorkerID = "another-worker"
	if err := x.s.RenewExecutionLease(context.Background(), stale, ob, x.now.Add(time.Minute), x.now); !errors.Is(err, domain.ErrSchedulerFence) {
		t.Fatalf("wrong worker renewed: %v", err)
	}
	ob.Readiness = ports.SessionReadinessUnknown
	if err := x.s.RenewExecutionLease(context.Background(), p, ob, x.now.Add(time.Minute), x.now); !errors.Is(err, domain.ErrWorkflowAttemptLeaseConflict) {
		t.Fatalf("unknown renewed: %v", err)
	}
	ob = x.observation(rec)
	if err := x.s.RenewExecutionLease(context.Background(), p, ob, x.now.Add(time.Minute), x.now); err != nil {
		t.Fatal(err)
	}
	if err := x.s.RenewExecutionLease(context.Background(), p, ob, x.now.Add(2*time.Minute), x.now); !errors.Is(err, domain.ErrSchedulerFence) {
		t.Fatalf("stale lease snapshot renewed: %v", err)
	}
	a, _, _ := x.s.GetWorkflowAttempt(context.Background(), x.attempt.ID)
	if a.HeartbeatAt == nil || !a.LeaseUntil.Equal(x.now.Add(time.Minute)) {
		t.Fatal("live heartbeat not persisted")
	}
	y := newExecutionFixture(t)
	r := y.accept(t)
	y.now = y.now.Add(21 * time.Second)
	q, _ := ports.ExecutionPermitFromContext(y.context(t))
	if err := y.s.RenewExecutionLease(context.Background(), q, y.observation(r), y.now.Add(time.Minute), y.now); !errors.Is(err, domain.ErrWorkflowAttemptLeaseConflict) {
		t.Fatalf("expired lease revived: %v", err)
	}
}

// 删除可信终止检查会提前释放运行槽；删除预算冻结会把尚未对账的费用当作已结清。
func TestM3ExecutionReleaseKeepsUnreconciledBudget(t *testing.T) {
	x := newExecutionFixture(t)
	rec := x.accept(t)
	ob := x.observation(rec)
	if err := x.s.ReleaseExecutionAdmission(context.Background(), x.ad, ob, x.now); !errors.Is(err, domain.ErrSchedulerAdmissionConflict) {
		t.Fatalf("live slot released: %v", err)
	}
	ob.Terminated = true
	if err := x.s.ReleaseExecutionAdmission(context.Background(), x.ad, ob, x.now); !errors.Is(err, domain.ErrSchedulerAdmissionConflict) {
		t.Fatalf("unpersisted termination accepted: %v", err)
	}
	rec.IsTerminated = true
	if err := x.s.UpdateSession(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	if err := x.s.ReleaseExecutionAdmission(context.Background(), x.ad, ob, x.now); err != nil {
		t.Fatal(err)
	}
	ad, _, _ := x.s.GetSchedulerAdmission(context.Background(), x.ad.ID)
	if ad.State != domain.AdmissionReleased {
		t.Fatal("terminated slot not released")
	}
	if _, err := x.s.ReconcileReleasedBudgetReservations(context.Background(), 100, x.now); err != nil {
		t.Fatal(err)
	}
	rs, err := x.s.ListBudgetReservations(context.Background(), x.ad.ID)
	if err != nil || len(rs) != 1 || rs[0].State != "uncertain" {
		t.Fatalf("unreconciled budget released: %+v %v", rs, err)
	}
}

// 删除全局恢复审计事务的重放分支会把原 count=1 变成 0；恢复不得释放 UNKNOWN 占位。
func TestM3AuthorizedExpiryRecoveryReplaysOriginalCount(t *testing.T) {
	x := newExecutionFixture(t)
	x.now = x.now.Add(21 * time.Second)
	change := domain.PolicyChange{IdempotencyKey: "operator-recovery", RequestFingerprint: strings.Repeat("a", 64), ActorRef: "operator", Reason: "OPERATOR_RECOVERY", Now: x.now}
	count, applied, err := x.s.RecoverExpiredWorkflowsAuthorized(context.Background(), change)
	if err != nil || !applied || count != 1 {
		t.Fatalf("first recovery: count=%d applied=%v err=%v", count, applied, err)
	}
	count, applied, err = x.s.RecoverExpiredWorkflowsAuthorized(context.Background(), change)
	if err != nil || applied || count != 1 {
		t.Fatalf("replayed recovery: count=%d applied=%v err=%v", count, applied, err)
	}
	ad, _, _ := x.s.GetSchedulerAdmission(context.Background(), x.ad.ID)
	if ad.State == domain.AdmissionReleased {
		t.Fatal("expiry released an unknown execution")
	}
	change.RequestFingerprint = strings.Repeat("b", 64)
	if _, _, err := x.s.RecoverExpiredWorkflowsAuthorized(context.Background(), change); !errors.Is(err, domain.ErrSchedulerPolicy) {
		t.Fatalf("reused approval key accepted different request: %v", err)
	}
}

// 删除 uncertain 占位的 live 拒绝会让租约尚存但执行事实未知的 Controller 获得动作许可。
func TestM3ExecutionUncertainAdmissionCannotAuthorizeActions(t *testing.T) {
	x := newExecutionFixture(t)
	x.accept(t)
	if _, _, err := x.s.AdoptSchedulerAdmission(context.Background(), x.ad.ID, x.ad.Revision, x.ad.Fence, x.now); err != nil {
		t.Fatal(err)
	}
	p, _ := ports.ExecutionPermitFromContext(x.context(t))
	if err := x.s.ValidateExecutionPermit(context.Background(), p, x.now); !errors.Is(err, domain.ErrSchedulerAdmissionConflict) {
		t.Fatalf("uncertain admission authorized an action: %v", err)
	}
}

// 删除已接受代际与 Binding 的一致性门禁会在新 Controller 尚未发布时给旧 Controller 续租。
func TestM3ExecutionUnpublishedRestartCannotRenewOldController(t *testing.T) {
	x := newExecutionFixture(t)
	rec := x.accept(t)
	if err := x.s.AdmitSessionRestart(x.context(t), rec, "generation-2", domain.AdmissionEvidence{Fence: x.ad.Fence, Now: x.now}); err != nil {
		t.Fatal(err)
	}
	p, _ := ports.ExecutionPermitFromContext(x.context(t))
	if err := x.s.RenewExecutionLease(context.Background(), p, x.observation(rec), x.now.Add(time.Minute), x.now); !errors.Is(err, domain.ErrSchedulerAdmissionConflict) {
		t.Fatalf("old controller renewed during replacement: %v", err)
	}
}
