package store_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// TestM4CollaborationReplayAtFullQueue 删除容量前幂等查询会拒绝交接重放；删除发送者比较会允许跨任务冒领。
func TestM4CollaborationReplayAtFullQueue(t *testing.T) {
	s := newTestStore(t)
	now := workflowStoreTime().Add(3 * time.Second)
	wid := domain.WorkflowID("m4-message-replay")
	attempts := collaborationFixture(t, s, wid, "m4-collab", "a", "b")
	ctx := sqlitetest.ExecutionContext(context.Background(), t, s, attempts["a"].ID, now)
	first := collaborationMessage(wid, attempts["a"], nil, "original")
	if _, _, err := s.CreateWorkflowMessage(ctx, first, now); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < domain.MaxOpenWorkflowMessages; i++ {
		m := collaborationMessage(wid, attempts["a"], nil, fmt.Sprint("other-", i))
		if _, _, err := s.CreateWorkflowMessage(ctx, m, now); err != nil {
			t.Fatal(err)
		}
	}
	if r, made, err := s.CreateWorkflowMessage(ctx, first, now); err != nil || made || r.ID != first.ID {
		t.Fatalf("full queue replay: %+v %v %v", r, made, err)
	}
	other := collaborationMessage(wid, attempts["b"], nil, "original")
	ctx = sqlitetest.ExecutionContext(context.Background(), t, s, attempts["b"].ID, now)
	if _, _, err := s.CreateWorkflowMessage(ctx, other, now); !errors.Is(err, domain.ErrWorkflowMessageConflict) {
		t.Fatalf("cross sender replay: %v", err)
	}
}

// TestM4TaskRequestReplayBindsRequesterAndKind 删除 requester/kind 比较会允许不同权限请求冒用相同摘要。
func TestM4TaskRequestReplayBindsRequesterAndKind(t *testing.T) {
	s := newTestStore(t)
	now := workflowStoreTime().Add(3 * time.Second)
	wid := domain.WorkflowID("m4-request-replay")
	attempts := collaborationFixture(t, s, wid, "m4-request", "a", "b")
	ctx := sqlitetest.ExecutionContext(context.Background(), t, s, attempts["a"].ID, now)
	r := domain.WorkflowTaskRequest{ID: "fix", WorkflowID: wid, RequesterTaskID: "a", RequestKind: domain.WorkflowTaskRequestKindCreateTask, RequestJSON: `{"title":"fix"}`, RequestSHA: collabSHA, WorkflowRevision: 2}
	if _, _, err := s.CreateWorkflowTaskRequest(ctx, r, now); err != nil {
		t.Fatal(err)
	}
	r.RequestKind = domain.WorkflowTaskRequestKindIncreaseBudget
	if _, _, err := s.CreateWorkflowTaskRequest(ctx, r, now); !errors.Is(err, domain.ErrWorkflowTaskRequestConflict) {
		t.Fatalf("kind replay: %v", err)
	}
	r.RequestKind = domain.WorkflowTaskRequestKindCreateTask
	r.RequesterTaskID = "b"
	ctx = sqlitetest.ExecutionContext(context.Background(), t, s, attempts["b"].ID, now)
	if _, _, err := s.CreateWorkflowTaskRequest(ctx, r, now); !errors.Is(err, domain.ErrWorkflowTaskRequestConflict) {
		t.Fatalf("requester replay: %v", err)
	}
}
