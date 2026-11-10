package sessionmanager

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type restoreContextAgent struct {
	terminalImportAgent
	hooks, runtimeHooks []ports.WorkspaceHookConfig
}

func (a *restoreContextAgent) GetAgentHooks(_ context.Context, cfg ports.WorkspaceHookConfig) error {
	a.hooks = append(a.hooks, cfg)
	return nil
}

func (a *restoreContextAgent) PrepareRuntimeLaunch(_ context.Context, cfg ports.WorkspaceHookConfig) error {
	a.runtimeHooks = append(a.runtimeHooks, cfg)
	return nil
}

func TestFreshWorkspaceHooksHaveNoNativeRestoreContext(t *testing.T) {
	a := &restoreContextAgent{}
	m := New(Deps{DataDir: t.TempDir()})
	if err := m.prepareWorkspace(context.Background(), a, "fresh", t.TempDir(), "", "", ports.AgentConfig{}, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if err := m.prepareRuntimeLaunch(context.Background(), a, "fresh", t.TempDir(), "", "", ports.AgentConfig{}, map[string]string{}, workspaceRestoreContext{}); err != nil {
		t.Fatal(err)
	}
	for _, cfg := range append(a.hooks, a.runtimeHooks...) {
		if cfg.NativeSessionID != "" || cfg.ImportConfigDir != "" {
			t.Fatalf("fresh launch received restore context: %#v", cfg)
		}
	}
}

func TestImportedWorkspaceHooksReceiveSelectedNativeIdentity(t *testing.T) {
	a := &restoreContextAgent{terminalImportAgent: terminalImportAgent{native: true}}
	m, st, _ := newExitedResumeManager(t, &fakeRuntime{}, a)
	rec := st.sessions["mer-1"]
	rec.Mode, rec.Activity.State = domain.SessionModeTUI, domain.ActivityIdle
	rec.Metadata.WorkspacePath = t.TempDir()
	rec.Metadata.RuntimeHandleID, rec.Metadata.RuntimeLaunchID = "", ""
	rec.Metadata.AgentSessionID, rec.Metadata.ProviderConversationID = "selected-native", "selected-native"
	rec.Metadata.ImportSource = &domain.SessionImportSource{NativeID: "selected-native", ConfigDir: t.TempDir(), Transferred: true, Prepared: true}
	st.sessions[rec.ID] = rec
	if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); err != nil {
		t.Fatal(err)
	}
	if len(a.hooks) != 1 || len(a.runtimeHooks) != 1 {
		t.Fatalf("hooks=%d runtime hooks=%d", len(a.hooks), len(a.runtimeHooks))
	}
	if cfg := a.runtimeHooks[0]; cfg.NativeSessionID != "selected-native" || cfg.ImportConfigDir != rec.Metadata.ImportSource.ConfigDir {
		t.Fatalf("imported restore lost selected identity: %#v", cfg)
	}
}

func TestWorkspaceRestoreContextUsesPreparedIdentityAndClearsForFresh(t *testing.T) {
	rec := domain.SessionRecord{Metadata: domain.SessionMetadata{AgentSessionID: "copied-id", ImportSource: &domain.SessionImportSource{NativeID: "source-id", ConfigDir: "/source", ResumeNativeID: "copied-id", ResumeConfigDir: "/private"}}}
	for _, adopted := range []bool{false, true} {
		rec.Metadata.ImportSource.Adopted = adopted
		if got := restoredWorkspaceContext(rec, false); got.nativeID != "copied-id" || got.configDir != "/private" {
			t.Fatalf("prepared restore context = %#v", got)
		}
	}
	if got := restoredWorkspaceContext(rec, true); got != (workspaceRestoreContext{}) {
		t.Fatalf("fresh fallback retained native restore fields: %#v", got)
	}
}
