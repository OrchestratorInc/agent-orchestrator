package execution_test

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// reanchorPermit 让旧代际回归显式持有当前 SQLite 许可；被测 revision 参数仍由用例控制。
func reanchorPermit(t *testing.T, s *sqlite.Store, id domain.WorkflowAttemptID) context.Context {
	t.Helper()
	leader, err := s.GetSchedulerLeader(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.LoadExecutionPermit(context.Background(), id, leader.Fence, reanchorNow)
	if err != nil {
		t.Fatal(err)
	}
	return ports.WithExecutionPermit(context.Background(), p)
}
