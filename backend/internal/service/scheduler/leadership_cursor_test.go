package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// pausedRenewStore 只在真实 SQLite 续租 CAS 前设置并发屏障，不伪造 Leader 或数据库结果。
type pausedRenewStore struct {
	ports.SchedulerStore
	renewEntered, renewResume   chan struct{}
	cursorEntered, cursorResume chan struct{}
	renewOnce                   sync.Once
}

// RenewSchedulerLeader 让真实 Tick 在读取 Leader revision 与写入 CAS 之间完成游标提交。
func (s *pausedRenewStore) RenewSchedulerLeader(ctx context.Context, expected domain.SchedulerLeader, now, until time.Time) (domain.SchedulerLeader, bool, error) {
	s.renewOnce.Do(func() {
		close(s.renewEntered)
		select {
		case <-s.renewResume:
		case <-ctx.Done():
		}
	})
	return s.SchedulerStore.RenewSchedulerLeader(ctx, expected, now, until)
}

// SetSchedulerCursor 在获取本地 Leader 锁后暂停 SQL，允许续租先读到旧 revision。
func (s *pausedRenewStore) SetSchedulerCursor(ctx context.Context, fence domain.SchedulerFence, revision int64, cursor string, now time.Time) (bool, error) {
	close(s.cursorEntered)
	select {
	case <-s.cursorResume:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	return s.SchedulerStore.SetSchedulerCursor(ctx, fence, revision, cursor, now)
}

// 同任期 Tick 改变 revision 是正常并发，不能使当前 daemon 忘记尚有效的自身任期。
func TestLeaderRenewalToleratesOwnCursorCAS(t *testing.T) {
	f := newFixture(t)
	f.create(1, domain.HarnessCodex)
	before, err := f.scheduler.CurrentFence(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	barrier := &pausedRenewStore{SchedulerStore: f.store, renewEntered: make(chan struct{}), renewResume: make(chan struct{}), cursorEntered: make(chan struct{}), cursorResume: make(chan struct{})}
	f.scheduler.deps.Store = barrier
	f.clock.Advance(10 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var cursorRelease, renewRelease sync.Once
	releaseCursor := func() { cursorRelease.Do(func() { close(barrier.cursorResume) }) }
	releaseRenew := func() { renewRelease.Do(func() { close(barrier.renewResume) }) }
	defer releaseCursor()
	defer releaseRenew()
	tickDone := make(chan error, 1)
	go func() {
		_, err := f.scheduler.Tick(ctx)
		tickDone <- err
	}()
	select {
	case <-barrier.cursorEntered:
	case <-ctx.Done():
		releaseCursor()
		<-tickDone
		t.Fatal("Tick did not reach its SQLite cursor barrier")
	}
	renewed := make(chan error, 1)
	go func() {
		_, err := f.scheduler.MaintainLeadership(ctx)
		renewed <- err
	}()
	select {
	case <-barrier.renewEntered:
	case <-ctx.Done():
		releaseCursor()
		releaseRenew()
		<-tickDone
		<-renewed
		t.Fatal("renewal did not reach the SQLite CAS barrier")
	}
	releaseCursor()
	tickErr := <-tickDone
	releaseRenew()
	renewErr := <-renewed
	if tickErr != nil {
		t.Fatalf("actual concurrent Tick failed: %v", tickErr)
	}
	persisted, err := f.store.GetSchedulerLeader(context.Background())
	if err != nil || persisted.Fence != before || !persisted.LeaseUntil.After(f.clock.Now()) {
		t.Fatalf("fixture lost durable leadership: %+v %v", persisted, err)
	}
	after, localErr := f.scheduler.CurrentFence(context.Background())
	if localErr != nil || after != before {
		t.Fatalf("own Tick changed only revision, but daemon discarded its live fence: renewal=%v local=%v persisted=%+v", renewErr, localErr, persisted)
	}
}
