package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// executionTestFence 捕获 fixture 已持有的任期，后续协调器调用不能重新读取并冒用其他 Leader。
func executionTestFence(t *testing.T, s *sqlite.Store) func(context.Context) (domain.SchedulerFence, error) {
	t.Helper()
	leader, err := s.GetSchedulerLeader(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return func(context.Context) (domain.SchedulerFence, error) { return leader.Fence, nil }
}

// admitCollaborationSession 通过真实准入与绑定事务建立 Agent 消息所需的会话身份，不启动 Provider。
func admitCollaborationSession(t *testing.T, s *sqlite.Store, attempt domain.WorkflowAttempt, projectID string, now time.Time) domain.WorkflowAttempt {
	t.Helper()
	ctx := context.Background()
	binding, repos := bindingIntent(attempt, projectID)
	binding, _, err := s.CreateWorkflowAttemptBinding(sqlitetest.ExecutionContext(ctx, t, s, attempt.ID, now), binding, repos, now)
	if err != nil {
		t.Fatal(err)
	}
	binding, _, err = s.TransitionWorkflowAttemptBinding(sqlitetest.ExecutionContext(ctx, t, s, attempt.ID, now), attempt.ID,
		domain.WorkflowBindingStateSpawnRequested, "", binding.Revision, now)
	if err != nil {
		t.Fatal(err)
	}
	admission, found, err := s.GetSchedulerAdmissionForAttempt(ctx, attempt.ID)
	if err != nil || !found {
		t.Fatalf("collaboration admission: found=%v err=%v", found, err)
	}
	record := sampleRecord(projectID)
	record.Harness = admission.Harness
	record.ClientRequestID = binding.SpawnRequestID
	record.ClientRequestHash = binding.SpawnHash
	record.CreatedAt, record.UpdatedAt = now, now
	session, _, err := s.CreateAdmittedSession(sqlitetest.ExecutionContext(ctx, t, s, attempt.ID, now), record, admission, "", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.BindWorkflowAttemptSession(sqlitetest.ExecutionContext(ctx, t, s, attempt.ID, now), attempt.ID, session.ID,
		"collaboration-generation", domain.WorkflowBindingStateBoundReady, binding.Revision, now); err != nil {
		t.Fatal(err)
	}
	attempt, found, err = s.GetWorkflowAttempt(ctx, attempt.ID)
	if err != nil || !found {
		t.Fatalf("collaboration bound attempt: found=%v err=%v", found, err)
	}
	return attempt
}
