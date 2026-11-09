package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// daemon 重启后 boot 恢复会把旧任期槽接管成 uncertain，再由 Coordinator.Reconcile/
// RefreshLeases 观测到 Session 已终止并释放该槽。Provider 切换曾让槽的代际落后于
// Session 已发布的代际；准入端已为此放行，但释放端仍拒绝同一条已终止 Session。
// 恢复因这一条记录持续报错，CompleteSchedulerRecovery 永远无法执行，调度器保持
// recovered=false，所有 Spawn 都以 "scheduler leadership fence is stale or unrecovered"
// 失败。删除放行会让整个 daemon 永久闭锁。
func TestTerminatedSessionReleasesDriftedSlotDuringRecovery(t *testing.T) {
	x := newRestartFixture(t)
	ctx := context.Background()
	// Provider 切换发布了新代际，随后该会话退出。
	x.rec.Metadata.ControllerGeneration = "switched"
	x.rec.IsTerminated = true
	x.rec.UpdatedAt = x.now.Add(time.Second)
	if err := x.s.UpdateSession(ctx, x.rec); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	// 新 Leader 接管旧任期槽，恢复先把它放成 uncertain。
	adopted, applied, err := x.s.AdoptSchedulerAdmission(ctx, x.ad.ID, x.ad.Revision, x.ad.Fence, x.now)
	if err != nil || !applied || adopted.State != domain.AdmissionUncertain {
		t.Fatalf("adopt: applied=%v state=%v err=%v", applied, adopted.State, err)
	}
	observation := ports.SessionObservation{
		SessionID: x.rec.ID, Terminated: true, ControllerGeneration: "switched",
		ClientRequestID: x.rec.ClientRequestID, SessionUpdatedAt: x.rec.UpdatedAt, ObservedAt: x.now,
	}
	if err := x.s.ReleaseExecutionAdmission(ctx, adopted, observation, x.now); err != nil {
		t.Fatalf("recovery refused to release a terminated session's stale slot: %v", err)
	}
	released, found, err := x.s.GetSchedulerAdmission(ctx, x.ad.ID)
	if err != nil || !found || released.State != domain.AdmissionReleased {
		t.Fatalf("slot after recovery = %+v found=%v err=%v", released, found, err)
	}
}

// 放行只覆盖已终止的普通 Session：live Session 的漂移槽仍可能是在途的未发布启动，
// 工作流槽仍必须走 Attempt 栅栏。
func TestLiveSessionStillRefusesDriftedSlotRelease(t *testing.T) {
	x := newRestartFixture(t)
	ctx := context.Background()
	x.rec.Metadata.ControllerGeneration = "switched"
	x.rec.UpdatedAt = x.now.Add(time.Second)
	if err := x.s.UpdateSession(ctx, x.rec); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	adopted, applied, err := x.s.AdoptSchedulerAdmission(ctx, x.ad.ID, x.ad.Revision, x.ad.Fence, x.now)
	if err != nil || !applied {
		t.Fatalf("adopt: applied=%v err=%v", applied, err)
	}
	observation := ports.SessionObservation{
		SessionID: x.rec.ID, Terminated: true, ControllerGeneration: "switched",
		ClientRequestID: x.rec.ClientRequestID, SessionUpdatedAt: x.rec.UpdatedAt, ObservedAt: x.now,
	}
	if err := x.s.ReleaseExecutionAdmission(ctx, adopted, observation, x.now); !errors.Is(err, domain.ErrSchedulerAdmissionConflict) {
		t.Fatalf("live drifted slot released: %v", err)
	}
}
