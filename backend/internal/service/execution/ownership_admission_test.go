package execution

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// admittedOwnershipReader 提供真实所有权读取替身所缺的持久 Admission 快照。
type admittedOwnershipReader struct {
	*fakeOwnershipReader
	admission domain.SchedulerAdmission
}

func (r admittedOwnershipReader) GetSchedulerAdmissionForAttempt(context.Context, domain.WorkflowAttemptID) (domain.SchedulerAdmission, bool, error) {
	return r.admission, true, nil
}

// 删除 Admission Harness 回退会拒绝已批准的默认 Harness；归属错配必须继续拒绝。
func TestM3OwnershipUsesClaimedHarness(t *testing.T) {
	base := newOwnershipReader()
	seedOwnership(base)
	task := base.tasks["wf-1"]["task-1"]
	task.AgentHarness = ""
	base.tasks["wf-1"]["task-1"] = task
	reader := admittedOwnershipReader{fakeOwnershipReader: base, admission: domain.SchedulerAdmission{AttemptID: "attempt-1", WorkflowID: "wf-1", TaskID: "task-1", Harness: domain.HarnessClaudeCode, State: domain.AdmissionReserved}}
	got, err := ResolveAttemptOwnership(context.Background(), reader, "wf-1", "attempt-1")
	if err != nil || got.Harness != domain.HarnessClaudeCode {
		t.Fatalf("claimed harness not retained: %s %v", got.Harness, err)
	}
	reader.admission.TaskID = "another-task"
	if _, err := ResolveAttemptOwnership(context.Background(), reader, "wf-1", "attempt-1"); err == nil {
		t.Fatal("foreign admission supplied the harness")
	}
}
