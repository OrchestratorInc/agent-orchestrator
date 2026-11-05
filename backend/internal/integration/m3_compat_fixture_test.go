package integration

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// m2FixtureFence 固定旧集成装配取得的任期，不能借用重启后其他实例的身份。
func m2FixtureFence(t *testing.T, st *store.Store, clock func() time.Time) func(context.Context) (domain.SchedulerFence, error) {
	t.Helper()
	sqlitetest.NewAdmissionFixture(t, st, clock)
	leader, err := st.GetSchedulerLeader(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// 旧 M2 装配没有 daemon 主循环；以真实续租事务维持其已取得的任期。
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				current, err := st.GetSchedulerLeader(context.Background())
				if err != nil || current.Fence != leader.Fence {
					return
				}
				now := clock()
				if current.LeaseUntil.Sub(now) <= 20*time.Second {
					if _, ok, err := st.RenewSchedulerLeader(context.Background(), current, now, now.Add(30*time.Second)); err != nil || !ok {
						return
					}
				}
			}
		}
	}()
	t.Cleanup(func() { close(stop); <-done })
	return func(ctx context.Context) (domain.SchedulerFence, error) {
		current, err := st.GetSchedulerLeader(ctx)
		if err != nil {
			return domain.SchedulerFence{}, err
		}
		if current.Fence != leader.Fence || !current.Recovered || !current.LeaseUntil.After(clock()) {
			return domain.SchedulerFence{}, domain.ErrSchedulerFence
		}
		return leader.Fence, nil
	}
}

// m2StoredLease 仅供内部领域断言读取；HTTP 不再返回或接受租约凭据。
func m2StoredLease(t *testing.T, st *store.Store, id domain.WorkflowAttemptID) string {
	t.Helper()
	a, found, err := st.GetWorkflowAttempt(context.Background(), id)
	if err != nil || !found {
		t.Fatalf("read attempt: found=%v err=%v", found, err)
	}
	return a.LeaseToken
}
