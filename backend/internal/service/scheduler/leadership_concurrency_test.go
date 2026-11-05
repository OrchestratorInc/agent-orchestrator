package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/workflow"
)

// TestLeadershipRenewsWhileClaimWaits 删除独立于调度锁的续租路径会使心跳阻塞，不能维持原任期。
func TestLeadershipRenewsWhileClaimWaits(t *testing.T) {
	f := newFixture(t)
	f.create(1, domain.HarnessCodex)
	entered, release := make(chan struct{}), make(chan struct{})
	original := f.scheduler.deps.Claim
	f.scheduler.deps.Claim = func(ctx context.Context, in workflow.ClaimInput) (workflow.ClaimResult, bool, error) {
		close(entered)
		<-release
		return original(ctx, in)
	}
	tickDone := make(chan error, 1)
	go func() { _, err := f.scheduler.Tick(context.Background()); tickDone <- err }()
	defer func() { close(release); <-tickDone }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("调度未进入领取")
	}
	f.clock.Advance(11 * time.Second)
	heartbeat := make(chan error, 1)
	go func() { _, err := f.scheduler.MaintainLeadership(context.Background()); heartbeat <- err }()
	select {
	case err := <-heartbeat:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("慢 Claim 阻塞了独立续租")
	}
	leader, err := f.store.GetSchedulerLeader(context.Background())
	if err != nil || !leader.LeaseUntil.Equal(f.clock.Now().Add(LeaderLease)) {
		t.Fatalf("租约未延长: %v %v", leader.LeaseUntil, err)
	}
}
