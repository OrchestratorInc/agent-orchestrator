package sessionmanager

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type importEnvAgent struct {
	preparedImportAgent
	seen         map[string]string
	mutate       bool
	augmentCalls int
}

func (a *importEnvAgent) AugmentRuntimeEnv(env map[string]string, _ string) {
	a.augmentCalls++
	env["SYNTHETIC_ADAPTER_ENV"] = "adapter-value"
}

func (a *importEnvAgent) PrepareImportedSession(_ context.Context, request ports.ImportedSessionRelocation) (ports.NativeSessionRef, error) {
	a.calls++
	a.seen = maps.Clone(request.Env)
	if a.mutate {
		request.Env["SYNTHETIC_WORKSPACE_ID"] = "adapter-mutation"
		delete(request.Env, "SYNTHETIC_CLEAR")
		request.Env[EnvBrowserCapability] = "adapter-token"
	}
	return a.ref, a.err
}

type importEnvAccounts struct {
	*transitionAccounts
	calls int
}

func (a *importEnvAccounts) SessionLaunchEnv(context.Context, domain.SessionID) (map[string]string, error) {
	a.calls++
	return map[string]string{"SYNTHETIC_WORKSPACE_ID": "selected-account", "SYNTHETIC_ACCOUNT_SECRET": "synthetic-account-secret-value"}, nil
}

func TestTerminalImportPreparationReusesResolvedEnvironment(t *testing.T) {
	ctx := context.Background()
	t.Setenv("SYNTHETIC_INHERITED_ONLY", "inherited-value")
	root, preparedRoot := t.TempDir(), t.TempDir()
	agent := &importEnvAgent{preparedImportAgent: preparedImportAgent{terminalImportAgent: terminalImportAgent{native: true}, ref: ports.NativeSessionRef{NativeSessionID: "agent-x", ConfigDir: preparedRoot}}, mutate: true}
	rt := &fakeRuntime{}
	manager, store, _ := newExitedResumeManager(t, rt, agent)
	project := store.projects["mer"]
	project.Config.Env = map[string]string{"SYNTHETIC_WORKSPACE_ID": "project-value", "SYNTHETIC_CLEAR": ""}
	store.projects[project.ID] = project
	accounts := &importEnvAccounts{}
	manager.agentAccounts = accounts
	issuer := &scriptedBrowserCapabilities{issues: []browserCapabilityIssue{{token: "synthetic-import-token", verifier: "synthetic-import-verifier"}}}
	manager.browserCapabilities = issuer
	record := store.sessions["mer-1"]
	record.Mode, record.Activity.State = domain.SessionModeTUI, domain.ActivityIdle
	record.Metadata.WorkspacePath = t.TempDir()
	record.Metadata.RuntimeHandleID, record.Metadata.RuntimeLaunchID = "", ""
	record.Metadata.ProviderConversationID = record.Metadata.AgentSessionID
	record.Metadata.ImportSource = &domain.SessionImportSource{NativeID: "agent-x", ConfigDir: root, CWD: t.TempDir(), Transferred: true, Prepared: true}
	store.sessions[record.ID] = record
	if _, err := manager.ResumeAgentWithMode(ctx, record.ID); err != nil {
		t.Fatal("native import with resolved launch environment failed")
	}
	if accounts.calls != 1 || issuer.calls != 1 || agent.augmentCalls != 1 || agent.calls != 1 {
		t.Fatalf("resolution/preparation counts = %d/%d/%d/%d", accounts.calls, issuer.calls, agent.augmentCalls, agent.calls)
	}
	if agent.seen["SYNTHETIC_WORKSPACE_ID"] != "selected-account" || agent.seen["SYNTHETIC_ADAPTER_ENV"] != "adapter-value" || agent.seen[EnvBrowserCapability] != "synthetic-import-token" {
		t.Fatal("preparer missed resolved account, adapter or capability evidence")
	}
	if value, present := agent.seen["SYNTHETIC_CLEAR"]; !present || value != "" {
		t.Fatal("preparer lost present-empty override semantics")
	}
	if _, inherited := agent.seen["SYNTHETIC_INHERITED_ONLY"]; inherited {
		t.Fatal("preparer expanded an inherited variable into the override map")
	}
	if rt.lastCfg.Env["SYNTHETIC_WORKSPACE_ID"] != "selected-account" || rt.lastCfg.Env[EnvBrowserCapability] != "synthetic-import-token" {
		t.Fatal("preparer mutation changed actual runtime launch environment")
	}
	if agent.seen["CODEX_HOME"] != root || rt.lastCfg.Env["CODEX_HOME"] != preparedRoot {
		t.Fatal("prepared provider root was not reapplied after native preparation")
	}
	if value, present := rt.lastCfg.Env["SYNTHETIC_CLEAR"]; !present || value != "" {
		t.Fatal("preparer mutation removed the actual launch's clear override")
	}
	encoded, err := json.Marshal(store.sessions[record.ID].Metadata)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "synthetic-account-secret-value") || strings.Contains(string(encoded), "synthetic-import-token") {
		t.Fatal("preparation environment secret was persisted in session metadata")
	}
}

func TestNonImportTerminalLaunchEnvironmentRemainsIdentical(t *testing.T) {
	ctx := context.Background()
	agent := &terminalImportAgent{native: true}
	rt := &fakeRuntime{}
	manager, store, _ := newExitedResumeManager(t, rt, agent)
	project := store.projects["mer"]
	project.Config.Env = map[string]string{"SYNTHETIC_WORKSPACE_ID": "project-value", "SYNTHETIC_CLEAR": ""}
	store.projects[project.ID] = project
	record := store.sessions["mer-1"]
	// Capture the existing non-import environment contract with deterministic
	// issuance and generation; the new preparation branch must not add fields.
	expected := manager.runtimeEnv(record.ID, record.ProjectID, record.IssueID, project.Config.Env)
	expected[EnvBrowserCapability] = "synthetic-ordinary-token"
	expected[EnvRuntimeLaunchID] = "launch-new"
	pinRuntimePermissionEnv(expected, restoredAgentConfig(record, project.Config).Permissions)
	manager.augmentRuntimePATHForLaunchBinary(ctx, expected, []string{"gemini"})
	issuer := &scriptedBrowserCapabilities{issues: []browserCapabilityIssue{{token: "synthetic-ordinary-token", verifier: "synthetic-ordinary-verifier"}}}
	manager.browserCapabilities = issuer
	if _, err := manager.ResumeAgentWithMode(ctx, record.ID); err != nil {
		t.Fatal("ordinary native restore failed")
	}
	if !maps.Equal(rt.lastCfg.Env, expected) || issuer.calls != 1 || len(agent.relocations) != 0 {
		t.Fatal("non-import launch environment or preparation order changed")
	}
}

func TestImportedPreparationEnvironmentIsClonedAndErrorsRedacted(t *testing.T) {
	ctx := context.Background()
	agent := &importEnvAgent{preparedImportAgent: preparedImportAgent{err: errors.New("failed with synthetic-private-value")}, mutate: true}
	manager, store, _ := newExitedResumeManager(t, &fakeRuntime{}, agent)
	record := store.sessions["mer-1"]
	record.Metadata.ImportSource = &domain.SessionImportSource{NativeID: "source", ConfigDir: t.TempDir(), Prepared: true}
	store.sessions[record.ID] = record
	env := map[string]string{"SYNTHETIC_WORKSPACE_ID": "project-value", "SYNTHETIC_CLEAR": "", "SYNTHETIC_SECRET": "synthetic-private-value"}
	before := maps.Clone(env)
	_, err := manager.relocateImportedSession(ctx, record, t.TempDir(), env)
	if err == nil || strings.Contains(err.Error(), "synthetic-private-value") || !maps.Equal(env, before) {
		t.Fatal("preparation leaked a secret or mutated the retained launch environment")
	}
}

func TestChatImportPreparationReusesEnvironmentThroughControllerGate(t *testing.T) {
	launcher := &recordingLauncher{}
	manager, store, _ := newChatManager(launcher)
	manager.dataDir = t.TempDir()
	seedChatResumeSession(store, domain.ActivityExited)
	root := t.TempDir()
	agent := &importEnvAgent{preparedImportAgent: preparedImportAgent{ref: ports.NativeSessionRef{NativeSessionID: "thread-1", ConfigDir: root}}, mutate: true}
	manager.agents = testAgentRegistry{domain.HarnessCodex: agent}
	record := store.sessions["mer-1"]
	record.Metadata.WorkspacePath = t.TempDir()
	record.Metadata.AgentSessionID, record.Metadata.ProviderConversationID = "thread-1", "thread-1"
	record.Metadata.ImportSource = &domain.SessionImportSource{NativeID: "thread-1", ConfigDir: root, Transferred: true, Prepared: true}
	store.sessions[record.ID] = record
	project := store.projects[string(record.ProjectID)]
	project.Config.Env = map[string]string{"SYNTHETIC_WORKSPACE_ID": "project-value", "SYNTHETIC_CLEAR": ""}
	project.Config.Worker = domain.RoleOverride{Harness: domain.HarnessCodex, AgentConfig: domain.AgentConfig{Model: "gpt-synthetic"}}
	store.projects[project.ID] = project
	issuer := &scriptedBrowserCapabilities{issues: []browserCapabilityIssue{{token: "synthetic-chat-token", verifier: "synthetic-chat-verifier"}}}
	manager.browserCapabilities = issuer
	if _, err := manager.ResumeAgentWithMode(context.Background(), record.ID); err != nil {
		t.Fatal("imported Chat preparation/controller launch failed")
	}
	if issuer.calls != 1 || agent.calls != 1 || agent.augmentCalls != 1 || len(launcher.started) != 1 {
		t.Fatalf("Chat resolution/preparation counts = %d/%d/%d/%d", issuer.calls, agent.calls, agent.augmentCalls, len(launcher.started))
	}
	actual := launcher.started[0].Env
	if actual[EnvBrowserCapability] != "synthetic-chat-token" || actual["SYNTHETIC_WORKSPACE_ID"] != "project-value" || agent.seen[EnvBrowserCapability] != actual[EnvBrowserCapability] {
		t.Fatal("Chat controller did not reuse the preparer's resolved environment")
	}
	if value, present := actual["SYNTHETIC_CLEAR"]; !present || value != "" {
		t.Fatal("Chat preparation mutation removed a clear override")
	}
	if store.sessions[record.ID].Metadata.BrowserCapabilityVerifier != "synthetic-chat-verifier" {
		t.Fatal("retained Chat capability was not persisted for the controller")
	}
	if launcher.started[0].Model != "gpt-synthetic" || store.sessions[record.ID].Metadata.Model != "gpt-synthetic" {
		t.Fatalf("import did not open on the project model: %q, %q", launcher.started[0].Model, store.sessions[record.ID].Metadata.Model)
	}
}

type importEnvRelocator struct {
	terminalImportAgent
}

func (a *importEnvRelocator) RelocateImportedSession(_ context.Context, request ports.ImportedSessionRelocation) error {
	a.relocations = append(a.relocations, request)
	request.Env["SYNTHETIC_CLEAR"] = "mutation"
	return nil
}

func TestLegacyImportRelocationGetsEnvironmentClone(t *testing.T) {
	agent := &importEnvRelocator{}
	manager, store, _ := newExitedResumeManager(t, &fakeRuntime{}, agent)
	record := store.sessions["mer-1"]
	record.Metadata.ImportSource = &domain.SessionImportSource{NativeID: "source", ConfigDir: t.TempDir(), Prepared: true}
	store.sessions[record.ID] = record
	env := map[string]string{"SYNTHETIC_CLEAR": ""}
	if _, err := manager.relocateImportedSession(context.Background(), record, t.TempDir(), env); err != nil {
		t.Fatal("legacy relocation failed")
	}
	if env["SYNTHETIC_CLEAR"] != "" || len(agent.relocations) != 1 {
		t.Fatal("legacy relocator mutated the retained launch environment")
	}
}

func TestNonImportChatLaunchEnvironmentRemainsIdentical(t *testing.T) {
	launcher := &recordingLauncher{}
	manager, store, _ := newChatManager(launcher)
	manager.dataDir = t.TempDir()
	seedChatResumeSession(store, domain.ActivityExited)
	record := store.sessions["mer-1"]
	project := store.projects[string(record.ProjectID)]
	project.Config.Env = map[string]string{"SYNTHETIC_WORKSPACE_ID": "project-value", "SYNTHETIC_CLEAR": ""}
	store.projects[project.ID] = project
	expected := manager.runtimeEnv(record.ID, record.ProjectID, record.IssueID, project.Config.Env)
	expected[EnvBrowserCapability] = "synthetic-ordinary-chat-token"
	issuer := &scriptedBrowserCapabilities{issues: []browserCapabilityIssue{{token: "synthetic-ordinary-chat-token", verifier: "synthetic-ordinary-chat-verifier"}}}
	manager.browserCapabilities = issuer
	if _, err := manager.ResumeAgentWithMode(context.Background(), record.ID); err != nil {
		t.Fatal("ordinary Chat restore failed")
	}
	if len(launcher.started) != 1 || !maps.Equal(launcher.started[0].Env, expected) || issuer.calls != 1 {
		t.Fatal("non-import Chat launch environment changed")
	}
}

func TestImportEnvironmentFailurePrecedesNativePreparation(t *testing.T) {
	agent := &importEnvAgent{}
	rt := &fakeRuntime{}
	manager, store, _ := newExitedResumeManager(t, rt, agent)
	record := store.sessions["mer-1"]
	record.Mode, record.Activity.State = domain.SessionModeTUI, domain.ActivityIdle
	record.Metadata.WorkspacePath = t.TempDir()
	record.Metadata.RuntimeHandleID, record.Metadata.RuntimeLaunchID = "", ""
	record.Metadata.ImportSource = &domain.SessionImportSource{NativeID: "agent-x", ConfigDir: t.TempDir(), Transferred: true, Prepared: true}
	store.sessions[record.ID] = record
	manager.browserCapabilities = &scriptedBrowserCapabilities{issues: []browserCapabilityIssue{{err: errors.New("synthetic issuance failure")}}}
	if _, err := manager.ResumeAgentWithMode(context.Background(), record.ID); err == nil {
		t.Fatal("import launched after environment preparation failed")
	}
	if agent.calls != 0 || rt.created != 0 || store.sessions[record.ID].Metadata.ImportSource.ResumeNativeID != "" {
		t.Fatal("failed environment preparation changed native provider or launch state")
	}
}
