package sessionmanager

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// 删除空 closer 判定会 panic；改用 Kill 或跳过真实 Stop 会破坏工作树或漏停 Controller。
func TestWorkflowRetirementWithoutShellCloser(t *testing.T) {
	launcher := &recordingLauncher{live: true}
	m, st := newWorkflowResumeManager(t, launcher)
	boundWorkflowSession(st, domain.WorkflowBindingStateBoundReady, "", nil)
	st.attempt.State = domain.WorkflowAttemptStateSucceeded
	before := st.sessions["mer-1"]
	st.attempt.SessionID = &before.ID
	if err := m.RetireWorkflowSession(context.Background(), before.ID, st.binding.ControllerGeneration); err != nil {
		t.Fatal(err)
	}
	after := st.sessions[before.ID]
	if !after.IsTerminated || after.Metadata.WorkspacePath != before.Metadata.WorkspacePath || after.Metadata.ProviderConversationID != before.Metadata.ProviderConversationID {
		t.Fatalf("retirement lost state: %+v", after)
	}
	if len(launcher.stopped) != 1 {
		t.Fatalf("stopped=%v", launcher.stopped)
	}
	if err := m.RetireWorkflowSession(context.Background(), before.ID, st.binding.ControllerGeneration); err != nil {
		t.Fatal(err)
	}
	if len(launcher.stopped) != 1 {
		t.Fatal("replay stopped twice")
	}
}

// GetWorkflowTask 补齐原恢复夹具的持久 task 身份，不使旧测试绕过新的只读标记读取。
func (s *workflowBindingFakeStore) GetWorkflowTask(_ context.Context, w domain.WorkflowID, id domain.WorkflowTaskID) (domain.WorkflowTask, bool, error) {
	return domain.WorkflowTask{ID: id, WorkflowID: w}, s.attemptFound, s.err
}
