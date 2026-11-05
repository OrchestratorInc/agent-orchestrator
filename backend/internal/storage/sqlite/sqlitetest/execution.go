package sqlitetest

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ExecutionStore 保留真实许可快照读取，兼容包裹 SQLite 的故障注入夹具。
type ExecutionStore interface {
	GetSchedulerLeader(context.Context) (domain.SchedulerLeader, error)
	LoadExecutionPermit(context.Context, domain.WorkflowAttemptID, domain.SchedulerFence, time.Time) (domain.ExecutionPermit, error)
}

// ExecutionContext 为单次测试写显式加载内部许可；不续租、不接管任期，也不创建缺失 Admission。
func ExecutionContext(t testing.TB, ctx context.Context, s ExecutionStore, id domain.WorkflowAttemptID, now time.Time) context.Context {
	t.Helper()
	leader, err := s.GetSchedulerLeader(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.LoadExecutionPermit(ctx, id, leader.Fence, now)
	if err != nil {
		t.Fatal(err)
	}
	return ports.WithExecutionPermit(ctx, p)
}
