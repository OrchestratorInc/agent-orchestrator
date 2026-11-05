package sessionmanager

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// newAdmittedTestManager 给真实 SQLite 夹具显式注入可信准入证据，不更改生产默认值。
func newAdmittedTestManager(t *testing.T, deps Deps) *Manager {
	t.Helper()
	m := New(deps)
	if store, ok := deps.Store.(sqlitetest.AdmissionStore); ok {
		m.SetSessionAdmissionProvider(sqlitetest.NewAdmissionFixture(t, store, m.clock))
	}
	return m
}

// managerExecutionPermit 让工作区回归在 Store 写入时持有真实快照，仍由原用例控制 CAS 参数。
func managerExecutionPermit(t *testing.T, s *sqlite.Store, id domain.WorkflowAttemptID, now time.Time) context.Context {
	t.Helper()
	leader, err := s.GetSchedulerLeader(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.LoadExecutionPermit(context.Background(), id, leader.Fence, now)
	if err != nil {
		t.Fatal(err)
	}
	return ports.WithExecutionPermit(context.Background(), p)
}
