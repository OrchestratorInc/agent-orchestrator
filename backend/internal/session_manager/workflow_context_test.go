package sessionmanager

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	browsersvc "github.com/aoagents/agent-orchestrator/backend/internal/service/browser"
)

// Recovering a workflow session's identity across a restart.
//
// A resumed session has no spawn config, so the attempt is reconstructed from durable
// state: the binding names the attempt, and the attempt row holds the lease. These
// tests drive the REAL resume path through the package's recordingLauncher and assert
// on the MCP server env the daemon would hand the agent's tool server.

// workflowBindingFakeStore adds the two workflow reads a resume needs. It embeds
// fakeStore so every other Manager dependency keeps working unchanged.
type workflowBindingFakeStore struct {
	*fakeStore
	binding domain.WorkflowAttemptBinding
	found   bool
	attempt domain.WorkflowAttempt
	// attemptFound distinguishes "no attempt row" from "an attempt with no lease".
	attemptFound bool
	err          error
}

func (s *workflowBindingFakeStore) GetWorkflowAttemptBindingBySession(
	_ context.Context,
	_ domain.SessionID,
) (domain.WorkflowAttemptBinding, bool, error) {
	if s.err != nil {
		return domain.WorkflowAttemptBinding{}, false, s.err
	}
	return s.binding, s.found, nil
}

// GetWorkflowAttemptBindingBySpawnRequest 本夹具只模拟已绑定的 Legacy Workflow，不提供 Spawn 意图。
func (s *workflowBindingFakeStore) GetWorkflowAttemptBindingBySpawnRequest(
	_ context.Context, _ string,
) (domain.WorkflowAttemptBinding, bool, error) {
	return domain.WorkflowAttemptBinding{}, false, s.err
}

// ListWorkflowAttemptRepoBindings 明确声明没有显式 Repo，保留 Resume 的 context 装配覆盖。
func (s *workflowBindingFakeStore) ListWorkflowAttemptRepoBindings(
	_ context.Context, _ domain.WorkflowAttemptID,
) ([]domain.WorkflowAttemptRepoBinding, error) {
	return nil, s.err
}

func (s *workflowBindingFakeStore) GetWorkflowAttempt(
	_ context.Context,
	_ domain.WorkflowAttemptID,
) (domain.WorkflowAttempt, bool, error) {
	if s.err != nil {
		return domain.WorkflowAttempt{}, false, s.err
	}
	return s.attempt, s.attemptFound, nil
}

// newWorkflowResumeManager builds a chat Manager whose store can answer the workflow
// reads, over the same fakes the other resume tests use.
func newWorkflowResumeManager(t *testing.T, launcher *recordingLauncher) (*Manager, *workflowBindingFakeStore) {
	t.Helper()
	mgr, store, _ := newChatManager(launcher)
	wrapped := &workflowBindingFakeStore{fakeStore: store}
	mgr.store = wrapped
	mgr.browserCapabilities = browsersvc.NewAuthority()
	seedChatResumeSession(store, domain.ActivityExited)
	return mgr, wrapped
}

// boundWorkflowSession gives the store a bound attempt for the seeded session.
func boundWorkflowSession(
	store *workflowBindingFakeStore,
	state domain.WorkflowBindingState,
	lease string,
	leaseUntil *time.Time,
) {
	sessionID := domain.SessionID("mer-1")
	store.found = true
	store.binding = domain.WorkflowAttemptBinding{
		AttemptID:            "attempt-resume",
		WorkflowID:           "workflow-resume",
		TaskID:               "task-resume",
		State:                state,
		SessionID:            &sessionID,
		Revision:             1,
		ControllerGeneration: "generation-before-resume",
	}
	rec := store.sessions[sessionID]
	rec.Metadata.ControllerGeneration = store.binding.ControllerGeneration
	store.sessions[sessionID] = rec
	store.attemptFound = true
	store.attempt = domain.WorkflowAttempt{
		ID:         "attempt-resume",
		WorkflowID: "workflow-resume",
		TaskID:     "task-resume",
		LeaseToken: lease,
		LeaseUntil: leaseUntil,
		State:      domain.WorkflowAttemptStateClaimed,
	}
}

// mcpEnvOf returns the `ao` tool server's env from a recorded start.
func mcpEnvOf(t *testing.T, cfg ports.ChatControllerStart) map[string]string {
	t.Helper()
	for _, server := range cfg.MCPServers {
		if server.Name == "ao" {
			return server.Env
		}
	}
	t.Fatal("the start carried no ao tool server")
	return nil
}

// TestResumeRecoversWorkflowMCPEnvFromDurableState is the product hole this closes:
// after a restart, Agent A can still message Agent B.
func TestResumeRecoversWorkflowMCPEnvFromDurableState(t *testing.T) {
	launcher := &recordingLauncher{}
	mgr, store := newWorkflowResumeManager(t, launcher)
	future := time.Now().UTC().Add(10 * time.Minute)
	boundWorkflowSession(store, domain.WorkflowBindingStateBoundReady, "live-lease", &future)
	project := store.projects[string(chatTestProject)]
	project.Config.Env = map[string]string{EnvWorkflowLease: "legacy-project-lease"}
	store.projects[string(chatTestProject)] = project

	if _, err := mgr.ResumeAgentWithMode(context.Background(), "mer-1"); err != nil {
		t.Fatalf("ResumeAgentWithMode: %v", err)
	}
	if len(launcher.started) != 1 {
		t.Fatalf("started %d controllers, want 1", len(launcher.started))
	}
	env := mcpEnvOf(t, launcher.started[0])

	want := map[string]string{
		EnvWorkflowID:        "workflow-resume",
		EnvWorkflowTaskID:    "task-resume",
		EnvWorkflowAttemptID: "attempt-resume",
	}
	for key, value := range want {
		if got := env[key]; got != value {
			t.Errorf("%s = %q, want %q", key, got, value)
		}
	}
	for _, captured := range []map[string]string{env, launcher.started[0].Env} {
		if _, present := captured[EnvWorkflowLease]; present || containsAny(mcpEnvText(captured), "live-lease", "legacy-project-lease") {
			t.Fatal("resume sent a lease to the agent or MCP environment")
		}
	}
	// The capability is the same one this launch minted for the agent itself.
	if got := env[EnvBrowserCapability]; got == "" {
		t.Error("the tool server env carries no capability")
	}
	if got := env[EnvBrowserCapability]; got != launcher.started[0].Env[EnvBrowserCapability] {
		t.Error("the tool server capability is not the one minted for this launch")
	}
	// The prompts carry none of it.
	if text := launcher.started[0].SystemPrompt; containsAny(text, "live-lease", "workflow-resume") {
		t.Error("the system prompt carries workflow identity or the lease")
	}
}

// TestResumeOmitsAnExpiredLease 要求过期绑定恢复失败关闭，不降为普通 Session 启动。
func TestResumeOmitsAnExpiredLease(t *testing.T) {
	launcher := &recordingLauncher{}
	mgr, store := newWorkflowResumeManager(t, launcher)
	past := time.Now().UTC().Add(-time.Minute)
	boundWorkflowSession(store, domain.WorkflowBindingStateBoundReady, "expired-lease", &past)

	if _, err := mgr.ResumeAgentWithMode(context.Background(), "mer-1"); err == nil {
		t.Fatal("expired workflow identity resumed")
	} else if containsAny(err.Error(), "expired-lease") {
		t.Fatal("resume error disclosed the lease")
	}
	if len(launcher.started) != 0 {
		t.Fatal("expired workflow identity started a controller")
	}
}

// TestResumeWithoutABindingAddsNoWorkflowKeys is the regression pin for ordinary
// sessions: nothing about them changes.
func TestResumeWithoutABindingAddsNoWorkflowKeys(t *testing.T) {
	launcher := &recordingLauncher{}
	mgr, store := newWorkflowResumeManager(t, launcher)
	store.found = false

	if _, err := mgr.ResumeAgentWithMode(context.Background(), "mer-1"); err != nil {
		t.Fatalf("ResumeAgentWithMode: %v", err)
	}
	env := mcpEnvOf(t, launcher.started[0])
	for _, key := range []string{
		EnvWorkflowID, EnvWorkflowTaskID, EnvWorkflowAttemptID, EnvWorkflowLease,
	} {
		if _, present := env[key]; present {
			t.Fatalf("a session with no binding carries %s", key)
		}
	}
}

// TestWorkflowSpawnContextRequiresABoundSession covers the state rule directly.
func TestWorkflowSpawnContextRequiresABoundSession(t *testing.T) {
	future := time.Now().UTC().Add(10 * time.Minute)
	for _, state := range []domain.WorkflowBindingState{
		domain.WorkflowBindingStateUnbound,
		domain.WorkflowBindingStateIntentPersisted,
		domain.WorkflowBindingStateSpawnRequested,
	} {
		launcher := &recordingLauncher{}
		mgr, store := newWorkflowResumeManager(t, launcher)
		boundWorkflowSession(store, state, "lease-1", &future)

		context, err := mgr.workflowSpawnContext(context.Background(), store.sessions["mer-1"])
		if err == nil || context != nil {
			t.Fatalf("%s must reject an invalid bound identity: context=%v err=%v", state, context, err)
		}
	}
}

// mcpEnvText flattens an env for a "secret appears nowhere" assertion.
func mcpEnvText(env map[string]string) string {
	var out string
	for key, value := range env {
		out += key + "=" + value + "\n"
	}
	return out
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if needle == "" {
			continue
		}
		for i := 0; i+len(needle) <= len(text); i++ {
			if text[i:i+len(needle)] == needle {
				return true
			}
		}
	}
	return false
}
