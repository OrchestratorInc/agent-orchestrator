package sessionmanager

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestWorkflowSpawnContextRejectsUntrustedDurableIdentity 删除对应归属、generation
// 或状态白名单校验会让不可信的持久绑定恢复为可用 MCP 身份。
func TestWorkflowSpawnContextRejectsUntrustedDurableIdentity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*workflowBindingFakeStore)
	}{
		{"uncertain binding", func(s *workflowBindingFakeStore) { s.binding.State = domain.WorkflowBindingStateReconcileRequired }},
		{"unknown attempt", func(s *workflowBindingFakeStore) { s.attempt.State = domain.WorkflowAttemptStateUnknown }},
		{"wrong session", func(s *workflowBindingFakeStore) { id := domain.SessionID("other"); s.binding.SessionID = &id }},
		{"wrong task", func(s *workflowBindingFakeStore) { s.attempt.TaskID = "other" }},
		{"wrong generation", func(s *workflowBindingFakeStore) { s.binding.ControllerGeneration = "stale" }},
		{"missing generation", func(s *workflowBindingFakeStore) { s.binding.ControllerGeneration = "" }},
		{"missing attempt", func(s *workflowBindingFakeStore) { s.attemptFound = false }},
		{"terminated session", func(s *workflowBindingFakeStore) {
			rec := s.sessions["mer-1"]
			rec.IsTerminated = true
			s.sessions["mer-1"] = rec
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mgr, store := newWorkflowResumeManager(t, &recordingLauncher{})
			future := time.Now().UTC().Add(time.Hour)
			boundWorkflowSession(store, domain.WorkflowBindingStateBoundReady, "internal-lease", &future)
			tc.mutate(store)
			identity, err := mgr.workflowSpawnContext(context.Background(), store.sessions["mer-1"])
			if err == nil || identity != nil {
				t.Fatalf("untrusted workflow context was accepted: identity=%v err=%v", identity, err)
			}
			if containsAny(err.Error(), "internal-lease") {
				t.Fatal("workflow identity error disclosed the internal lease")
			}
		})
	}
}
