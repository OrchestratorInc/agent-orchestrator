package pr

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func (f *fakeActionStore) HasWorkflowSessionOwnership(context.Context, domain.SessionID) (bool, error) {
	return false, nil
}

type workflowActionStore struct{ *fakeActionStore }

func (s workflowActionStore) HasWorkflowSessionOwnership(context.Context, domain.SessionID) (bool, error) {
	return true, nil
}

// TestWorkflowPRCannotBypassIntegration 删除 Merge 的绑定检查即会调用 SCM，绕过逐仓审批。
func TestWorkflowPRCannotBypassIntegration(t *testing.T) {
	pr, scm := mergeableActionFixture()
	pr.SessionID = "workflow-session"
	svc := NewActionService(ActionDeps{Store: workflowActionStore{&fakeActionStore{pr: pr, ok: true}}, Reader: scm, Merger: scm})
	_, err := svc.Merge(context.Background(), MergeRequest{PRID: "42", PRURL: pr.URL, ExpectedHeadSHA: pr.HeadSHA})
	if !errors.Is(err, ErrPRPreconditions) || scm.mergeCalls != 0 {
		t.Fatalf("workflow merge err=%v calls=%d", err, scm.mergeCalls)
	}
}
