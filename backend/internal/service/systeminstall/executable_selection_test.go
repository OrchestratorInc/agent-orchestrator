package systeminstall

import (
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestExecutableChangeExcludesQueuedUpdates 验证排队计划、启动和外部选择不能越过同一门禁。
func TestExecutableChangeExcludesQueuedUpdates(t *testing.T) {
	s := newTestService("windows", "npm")
	s.jobs[TargetDeepSeek] = &Job{Target: TargetDeepSeek, Status: StatusWaitingForIdle}
	if _, err := s.BeginExecutableChange(t.Context(), domain.HarnessDeepSeek); !errors.Is(err, ErrInstallActive) {
		t.Fatalf("queued update allowed selection: %v", err)
	}
	delete(s.jobs, TargetDeepSeek)
	release, err := s.BeginExecutableChange(t.Context(), domain.HarnessDeepSeek)
	if err != nil {
		t.Fatal(err)
	}
	if !harnessHeld(s, domain.HarnessDeepSeek) {
		t.Fatal("selection did not exclude launches")
	}
	if _, err := s.StartAgentOperation(t.Context(), TargetDeepSeek, "", AgentOperationInstall, ""); !errors.Is(err, ErrInstallActive) {
		t.Fatalf("install entered during selection: %v", err)
	}
	binaryutil.SetSelectedBinaries(map[string]string{"deepseek-harness": "external/dsh.cmd"})
	t.Cleanup(func() { binaryutil.SetSelectedBinaries(nil) })
	release()
	job, err := s.StartAgentOperation(t.Context(), TargetDeepSeek, "", AgentOperationInstall, "")
	if err != nil || job.Status != StatusUnsupported {
		t.Fatalf("external install = %+v, %v", job, err)
	}
	if harnessHeld(s, domain.HarnessDeepSeek) {
		t.Fatal("selection did not release launches")
	}
}
