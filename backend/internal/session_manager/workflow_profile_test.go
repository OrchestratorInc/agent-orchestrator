package sessionmanager

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// namingProfileStore 在真实 Manager 命名入口提供持久任务，首次调用尚无恢复绑定。
type namingProfileStore struct {
	*workflowBindingFakeStore
	task domain.WorkflowTask
}

// TestWorkflowResumePreservesDefaultEffortPolicy 恢复不能把项目默认值或 Provider 解析结果写回固定任务的空策略。
func TestWorkflowResumePreservesDefaultEffortPolicy(t *testing.T) {
	launcher := &recordingLauncher{}
	mgr, wrapped := newWorkflowResumeManager(t, launcher)
	mgr.dataDir = t.TempDir()
	mgr.profiles = namingProfiles{}
	rec := wrapped.sessions["mer-1"]
	rec.Metadata.Model, rec.Metadata.Effort = "fixed-model", ""
	wrapped.sessions[rec.ID] = rec
	project := wrapped.projects[string(rec.ProjectID)]
	project.Config.AgentConfig.Effort = "high"
	wrapped.projects[string(rec.ProjectID)] = project
	future := time.Now().UTC().Add(time.Hour)
	boundWorkflowSession(wrapped, domain.WorkflowBindingStateBoundReady, "lease", &future)
	store := &namingProfileStore{workflowBindingFakeStore: wrapped, task: domain.WorkflowTask{
		ID: "task-resume", WorkflowID: "workflow-resume", AgentHarness: rec.Harness,
		Profile: "fixed-profile", Model: "fixed-model", Effort: "",
	}}
	mgr.store = store
	if _, err := mgr.ResumeAgentWithMode(context.Background(), rec.ID); err != nil {
		t.Fatal(err)
	}
	if len(launcher.started) != 1 || launcher.started[0].Profile != store.task.Profile || launcher.started[0].Effort != "" || store.sessions[rec.ID].Metadata.Effort != store.task.Effort {
		t.Fatalf("default effort changed across resume: starts=%+v metadata=%+v", launcher.started, store.sessions[rec.ID].Metadata)
	}
}

func (s *namingProfileStore) GetWorkflowTask(context.Context, domain.WorkflowID, domain.WorkflowTaskID) (domain.WorkflowTask, bool, error) {
	return s.task, true, nil
}

// namingProfiles 只替换外部鉴权，保留 Manager 对持久选择的比较和环境隔离。
type namingProfiles struct{ ports.ProviderProfiles }

func (namingProfiles) Environment(context.Context, string, domain.AgentHarness, string, string) (map[string]string, error) {
	return map[string]string{ports.ProviderProfileKeyEnv: "test-only"}, nil
}

// TestWorkflowNamingAndBackgroundPreserveProfile 删除命名入口的 Profile 解析或清空固定 effort 会失败。
func TestWorkflowNamingAndBackgroundPreserveProfile(t *testing.T) {
	launcher := &taskNamingLauncher{result: `{"title":"Fix login","slug":"login"}`}
	m, st, _ := newChatManager(launcher)
	m.dataDir = t.TempDir()
	m.runBackground = func(work func()) { work() }
	m.profiles = namingProfiles{}
	store := &namingProfileStore{workflowBindingFakeStore: &workflowBindingFakeStore{fakeStore: st}, task: domain.WorkflowTask{
		ID: "task-resume", WorkflowID: "workflow-resume", AgentHarness: domain.HarnessCodex,
		Profile: "fixed-profile", Model: "fixed-model", Effort: "high",
	}}
	m.store = store
	cfg := asyncChatSpawnConfig("Fix login")
	cfg.Harness, cfg.NameFromPrompt, cfg.AgentConfigResolved = domain.HarnessCodex, true, true
	cfg.AgentConfig.Model, cfg.AgentConfig.Effort = store.task.Model, store.task.Effort
	cfg.WorkflowSpawn = &ports.WorkflowSpawnContext{WorkflowID: store.task.WorkflowID, TaskID: store.task.ID, AttemptID: "attempt-resume"}
	rec := domain.SessionRecord{ID: "mer-1", Harness: cfg.Harness, Metadata: domain.SessionMetadata{Model: store.task.Model, Effort: store.task.Effort}}
	st.sessions[rec.ID] = rec
	wait, stop := m.startTaskNaming(cfg, rec, st.projects["mer"].Config)
	if got := wait(); got.Title != "Fix login" {
		t.Fatalf("first naming did not execute: %+v", got)
	}
	stop()
	// 短调用只需持久归属；不恢复 Controller，也不要求工作 Attempt 的租约仍有效。
	store.found = true
	store.binding = domain.WorkflowAttemptBinding{WorkflowID: store.task.WorkflowID, TaskID: store.task.ID, SessionID: &rec.ID}
	if _, err := m.RunBackgroundTask(context.Background(), rec.ID, "title", "Fix login"); err != nil {
		t.Fatal(err)
	}
	if len(launcher.background) != 2 {
		t.Fatalf("calls=%d", len(launcher.background))
	}
	for _, call := range launcher.background {
		if call.Profile != store.task.Profile || call.Model != store.task.Model || call.Effort != store.task.Effort {
			t.Fatalf("fixed selection lost: %+v", call)
		}
	}
	store.task.Effort = "low"
	wait, stop = m.startTaskNaming(cfg, rec, st.projects["mer"].Config)
	defer stop()
	if got := wait(); got.Title != "" || len(launcher.background) != 2 {
		t.Fatal("changed persistent selection reached provider")
	}
}
