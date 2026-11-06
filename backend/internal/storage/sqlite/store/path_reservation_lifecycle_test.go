package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// 删除全仓冻结检查或恢复时全表接管会提前放开第二个 Repo，或丢失已 released Admission 的路径。
func TestM4PathReservationStoppedDeliveryAfterReleasedAdmission(t *testing.T) {
	x := newQualityFixture(t, 2)
	ctx := context.Background()
	if _, _, err := x.s.SaveArtifact(x.context(t), x.artifacts[0], x.ad.Fence, x.now); err != nil {
		t.Fatal(err)
	}
	x.stop(t)
	if n := qualityCount(t, x.db, `SELECT count(*) FROM workflow_task_path_reservations WHERE released_at IS NULL`); n != 4 {
		t.Fatalf("missing repo released paths=%d", n)
	}
	old := x.ad.Fence
	x.now = x.now.Add(31 * time.Second)
	leader, _, err := x.s.AcquireSchedulerLeader(ctx, "replacement", x.now, x.now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := x.s.SaveArtifact(ctx, x.artifacts[1], leader.Fence, x.now); !errors.Is(err, domain.ErrSchedulerFence) {
		t.Fatalf("unrecovered delivery: %v", err)
	}
	if _, _, err := x.s.CompleteSchedulerRecovery(ctx, leader.Fence, leader.Revision, x.now); err != nil {
		t.Fatal(err)
	}
	if n := qualityCount(t, x.db, `SELECT count(*) FROM workflow_task_path_reservations WHERE epoch=?`, leader.Fence.Epoch); n != 4 {
		t.Fatalf("released admission orphaned %d reservations", n)
	}
	if _, err := x.s.ReleasePathReservations(ctx, x.attempt.ID, old, x.now); !errors.Is(err, domain.ErrSchedulerFence) {
		t.Fatalf("old epoch released: %v", err)
	}
	if _, _, err := x.s.SaveArtifact(ctx, x.artifacts[1], leader.Fence, x.now); err != nil {
		t.Fatalf("trusted stopped delivery with expired lease: %v", err)
	}
	if released, err := x.s.ReleasePathReservations(ctx, x.attempt.ID, leader.Fence, x.now); err != nil || !released {
		t.Fatalf("frozen repos remained locked: %v %v", released, err)
	}
	if n := qualityCount(t, x.db, `SELECT count(*) FROM workflow_task_path_reservations WHERE released_at IS NULL`); n != 0 {
		t.Fatalf("partial release: %d", n)
	}
	a, _, err := x.s.GetWorkflowAttempt(ctx, x.attempt.ID)
	if err != nil || a.State != domain.WorkflowAttemptStateClaimed {
		t.Fatalf("termination inferred success: %+v %v", a, err)
	}
}

// 删除 UNKNOWN 拒绝或按 TTL 清理占位会使重启后的未知执行能够冻结/放开同一范围。
func TestM4PathReservationUnknownRestartRetainsAllPaths(t *testing.T) {
	x := newQualityFixture(t, 1)
	ctx := context.Background()
	if _, _, err := x.s.SaveArtifact(x.context(t), x.artifacts[0], x.ad.Fence, x.now); err != nil {
		t.Fatal(err)
	}
	x.now = x.now.Add(21 * time.Second)
	if n, err := x.s.RecoverExpiredWorkflowAttempts(ctx, x.now); err != nil || n != 1 {
		t.Fatalf("expire=%d %v", n, err)
	}
	x.stop(t)
	if err := x.s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlite.Open(x.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	x.s = reopened
	x.now = x.now.Add(11 * time.Second)
	leader, _, err := x.s.AcquireSchedulerLeader(ctx, "after-restart", x.now, x.now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := x.s.RecoverPathReservations(ctx, leader.Fence, x.now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := x.s.CompleteSchedulerRecovery(ctx, leader.Fence, leader.Revision, x.now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := x.s.SaveArtifact(ctx, x.artifacts[0], leader.Fence, x.now); !errors.Is(err, domain.ErrDeliveryBlocked) {
		t.Fatalf("UNKNOWN authorized delivery: %v", err)
	}
	if released, err := x.s.ReleasePathReservations(ctx, x.attempt.ID, leader.Fence, x.now); err != nil || released {
		t.Fatalf("UNKNOWN released: %v %v", released, err)
	}
	if n := qualityCount(t, x.db, `SELECT count(*) FROM workflow_task_path_reservations WHERE released_at IS NULL AND epoch=?`, leader.Fence.Epoch); n != 2 {
		t.Fatalf("restart lost occupancy: %d", n)
	}
}

// 删除任一代际同步或续租写入会留下旧占位；旧快照不能释放新的 Controller 所有权。
func TestM4PathReservationGenerationAndLeaseFollowBinding(t *testing.T) {
	x := newQualityFixture(t, 1)
	ctx := context.Background()
	if _, _, err := x.s.SaveArtifact(x.context(t), x.artifacts[0], x.ad.Fence, x.now); err != nil {
		t.Fatal(err)
	}
	b, _, err := x.s.GetWorkflowAttemptBinding(ctx, x.attempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := x.s.ClaimLiveChatControllerGeneration(ctx, x.rec.ID, "generation-2"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := x.s.ReanchorWorkflowAttemptGeneration(x.context(t), x.attempt.ID, x.rec.ID, "generation-2", b.Revision, b.State, x.now); err != nil {
		t.Fatal(err)
	}
	if n := qualityCount(t, x.db, `SELECT count(*) FROM workflow_task_path_reservations WHERE generation='generation-2'`); n != 2 {
		t.Fatalf("reanchor missed paths=%d", n)
	}
	p, err := x.s.LoadExecutionPermit(ctx, x.attempt.ID, x.ad.Fence, x.now)
	if err != nil {
		t.Fatal(err)
	}
	ob := x.observation(x.rec)
	ob.ControllerGeneration = "generation-2"
	until := x.now.Add(time.Minute)
	if err := x.s.RenewExecutionLease(ctx, p, ob, until, x.now); err != nil {
		t.Fatal(err)
	}
	var lease time.Time
	if err := x.db.QueryRow(`SELECT lease_until FROM workflow_task_path_reservations LIMIT 1`).Scan(&lease); err != nil {
		t.Fatal(err)
	}
	if !lease.Equal(until) {
		t.Fatalf("path lease=%s want=%s", lease, until)
	}
	// 重新附着走独立的同事务代际发布路径，不调用真实 Provider。
	x.rec, _, err = x.s.GetSession(ctx, x.rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	x.rec.Metadata.ProviderConversationID = "fixture-conversation"
	if err := x.s.UpdateSession(ctx, x.rec); err != nil {
		t.Fatal(err)
	}
	rp, err := x.s.PrepareSessionReattach(ctx, x.rec, x.ad.Fence, x.now)
	if err != nil {
		t.Fatal(err)
	}
	rctx := ports.WithSessionReattachPermit(ctx, rp, func() time.Time { return x.now })
	if err := x.s.ClaimLiveChatControllerGeneration(rctx, x.rec.ID, "generation-3"); err != nil {
		t.Fatal(err)
	}
	if n := qualityCount(t, x.db, `SELECT count(*) FROM workflow_task_path_reservations WHERE generation='generation-3'`); n != 2 {
		t.Fatalf("reattach missed paths=%d", n)
	}
	x.rec, _, err = x.s.GetSession(ctx, x.rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	x.rec.IsTerminated = true
	if err := x.s.UpdateSession(ctx, x.rec); err != nil {
		t.Fatal(err)
	}
	if released, err := x.s.ReleasePathReservations(ctx, x.attempt.ID, x.ad.Fence, x.now); err != nil || released {
		t.Fatalf("old generation artifact released: %v %v", released, err)
	}
}
