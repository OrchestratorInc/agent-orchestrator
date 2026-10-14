package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// sameReleases compares release calls field by field; reflect.DeepEqual on a
// struct holding an error is unreliable.
func sameReleases(a, b []releaseCall) bool {
	return slices.EqualFunc(a, b, func(x, y releaseCall) bool {
		return x.inUse == y.inUse && errors.Is(x.err, y.err)
	})
}

type releaseCall struct {
	inUse bool
	err   error
}

type fakeSandbox struct {
	ensureErr error
	ensured   []ports.SandboxRequest
	execEnv   map[string]string
	released  []releaseCall
	discarded []ports.SandboxTarget
	// sessionDiscards records DiscardSession calls as "project/session".
	sessionDiscards []string
	// noState lists sessions that never ran sandboxed; every other session
	// has sandbox state.
	noState map[domain.SessionID]bool
}

func (f *fakeSandbox) HasSessionState(_ domain.ProjectID, session domain.SessionID) bool {
	return !f.noState[session]
}

func (f *fakeSandbox) Discard(_ context.Context, target ports.SandboxTarget) error {
	f.discarded = append(f.discarded, target)
	return nil
}

func (f *fakeSandbox) DiscardSession(_ context.Context, project domain.ProjectID, session domain.SessionID) error {
	f.sessionDiscards = append(f.sessionDiscards, string(project)+"/"+string(session))
	return nil
}

func (f *fakeSandbox) Ensure(_ context.Context, req ports.SandboxRequest) (ports.SandboxTarget, error) {
	f.ensured = append(f.ensured, req)
	if f.ensureErr != nil {
		return ports.SandboxTarget{}, f.ensureErr
	}
	return ports.SandboxTarget{Container: "ao-sbx-" + string(req.SessionID), Docker: "/usr/bin/docker",
		BinDir: "/d/sandbox/bin", ProjectDir: "/d/sandbox/projects/p", SessionDir: "/d/s", RelayDir: "/d/s/relay", Mode: req.Config.Mode}, nil
}

func (f *fakeSandbox) Exec(t ports.SandboxTarget, ws string, env map[string]string, argv []string) ([]string, error) {
	f.execEnv = make(map[string]string, len(env))
	for k, v := range env {
		f.execEnv[k] = v
	}
	return append([]string{t.Docker, "exec", "-i", "-t", "-w", ws, t.Container, "/bin/sh", t.BinDir + "/ao-enter", t.SessionDir + "/env"}, argv...), nil
}

func (f *fakeSandbox) Release(ctx context.Context, _ domain.ProjectID, inUse func(context.Context) (bool, error)) error {
	used, err := inUse(ctx)
	f.released = append(f.released, releaseCall{inUse: used, err: err})
	return nil
}

// sandboxAgent is a launch-argv agent that can run sandboxed.
type sandboxAgent struct {
	launchArgvAgent
	prepared   []string // projectDir, workspacePath per PrepareSandbox call
	prepareErr error
}

func (*sandboxAgent) SandboxBinary() string { return "claude" }

func (a *sandboxAgent) PrepareSandbox(env map[string]string, _, projectDir, workspacePath string) error {
	a.prepared = append(a.prepared, projectDir, workspacePath)
	if a.prepareErr != nil {
		return a.prepareErr
	}
	env["CLAUDE_CONFIG_DIR"] = projectDir + "/claude"
	return nil
}

func sandboxedConfig(mode domain.SandboxMode) domain.ProjectConfig {
	cfg := testRoleAgents()
	cfg.Sandbox = domain.SandboxConfig{Mode: mode, Image: "node:22"}
	return cfg
}

func newSandboxSpawnManager(sb *fakeSandbox, agent ports.Agent) (*Manager, *fakeStore, *fakeRuntime) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repos/mer", Config: sandboxedConfig(domain.SandboxModePerAgent)}
	rt := &fakeRuntime{}
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st,
		Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, Sandbox: sb,
		LookPath:    func(string) (string, error) { return "/bin/true", nil },
		Executable:  func() (string, error) { return "/opt/ao", nil },
		NewLaunchID: func() string { return "launch-7" },
	})
	return m, st, rt
}

func TestWrapAgentProcessSandboxedForcesSupervisorWithRelay(t *testing.T) {
	m := New(Deps{Sandbox: &fakeSandbox{}, NewLaunchID: func() string { return "L1" },
		Executable: func() (string, error) { return "/opt/ao", nil }})
	sb := &sandboxLaunch{target: ports.SandboxTarget{Container: "c", Docker: "/usr/bin/docker", BinDir: "/b", SessionDir: "/s", RelayDir: "/s/relay"},
		workspacePath: "/w", binary: "claude"}
	env := map[string]string{}
	// fakeAgent does not implement AgentExitDetector, so only the sandbox
	// forces the supervisor here.
	argv, err := m.wrapAgentProcessWithLaunchID(fakeAgent{}, "s1", env, []string{"/Users/me/.local/bin/claude", "--x"}, "L1", false, sb)
	if err != nil {
		t.Fatal(err)
	}
	want := "/opt/ao agent-process supervise --session s1 --launch L1 --sandbox-relay /s/relay -- /usr/bin/docker exec -i -t -w /w c /bin/sh /b/ao-enter /s/env claude --x"
	if got := strings.Join(argv, " "); got != want {
		t.Fatalf("argv =\n%s\nwant\n%s", got, want)
	}
}

func TestWrapAgentProcessSandboxKeepsEnvWrapperArgv(t *testing.T) {
	if got := sandboxArgv([]string{"/usr/bin/env", "A=1", "claude"}, "claude"); !reflect.DeepEqual(got, []string{"/usr/bin/env", "A=1", "claude"}) {
		t.Fatalf("sandboxArgv rewrote an env wrapper: %#v", got)
	}
	if got := sandboxArgv([]string{"/x/claude", "--y"}, "claude"); !reflect.DeepEqual(got, []string{"claude", "--y"}) {
		t.Fatalf("sandboxArgv = %#v", got)
	}
}

func TestSpawnSandboxedLaunchesThroughDockerExec(t *testing.T) {
	sb := &fakeSandbox{}
	agent := &sandboxAgent{launchArgvAgent: launchArgvAgent{argv: []string{"/Users/me/.local/bin/claude", "--x"}}}
	m, _, rt := newSandboxSpawnManager(sb, agent)

	rec, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode})
	if err != nil {
		t.Fatal(err)
	}
	ws := rec.Metadata.WorkspacePath
	if len(sb.ensured) != 1 {
		t.Fatalf("ensure calls = %d, want 1", len(sb.ensured))
	}
	req := sb.ensured[0]
	if req.SessionID != "mer-1" || req.ProjectID != "mer" || req.RepoPath != "/repos/mer" || req.Config.Mode != domain.SandboxModePerAgent ||
		req.WorkspacePath != ws || !reflect.DeepEqual(req.RequireBinaries, []string{"claude"}) {
		t.Fatalf("ensure request = %+v", req)
	}
	if !reflect.DeepEqual(agent.prepared, []string{"/d/sandbox/projects/p", ws}) {
		t.Fatalf("prepare sandbox calls = %v", agent.prepared)
	}
	want := []string{"/opt/ao", "agent-process", "supervise", "--session", "mer-1", "--launch", "launch-7",
		"--sandbox-relay", "/d/s/relay", "--",
		"/usr/bin/docker", "exec", "-i", "-t", "-w", ws, "ao-sbx-mer-1", "/bin/sh", "/d/sandbox/bin/ao-enter", "/d/s/env",
		"claude", "--x"}
	if !reflect.DeepEqual(rt.lastCfg.Argv, want) {
		t.Fatalf("runtime argv =\n%#v\nwant\n%#v", rt.lastCfg.Argv, want)
	}
	if sb.execEnv[EnvRuntimeLaunchID] != "launch-7" || sb.execEnv["CLAUDE_CONFIG_DIR"] != "/d/sandbox/projects/p/claude" {
		t.Fatalf("sandbox env missing launch id or harness state: %v", sb.execEnv)
	}
	if len(sb.discarded) != 0 || m.sandboxInFlight.active("mer") {
		t.Fatalf("successful spawn discarded=%d inFlight=%v, want the container kept and no launch in flight", len(sb.discarded), m.sandboxInFlight.active("mer"))
	}
}

func TestSpawnSandboxUnavailableDoesNotLaunchOnHost(t *testing.T) {
	sb := &fakeSandbox{ensureErr: fmt.Errorf("%w: docker down", ports.ErrSandboxUnavailable)}
	agent := &sandboxAgent{launchArgvAgent: launchArgvAgent{argv: []string{"claude"}}}
	m, _, rt := newSandboxSpawnManager(sb, agent)

	_, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, RequestedMode: domain.SessionModeTUI})
	if !errors.Is(err, ports.ErrSandboxUnavailable) {
		t.Fatalf("spawn err = %v, want ErrSandboxUnavailable", err)
	}
	if rt.created != 0 {
		t.Fatalf("runtime Create calls = %d, want 0: a sandboxed spawn must never fall back to the host", rt.created)
	}
}

// A multi-repo workspace project's child worktrees have agent-writable .git
// files, so the agent could point AO's host git at its own config.
func TestPrepareSandboxRefusesWorkspaceProject(t *testing.T) {
	sb := &fakeSandbox{}
	agent := &sandboxAgent{launchArgvAgent: launchArgvAgent{argv: []string{"claude"}}}
	m, _, _ := newSandboxSpawnManager(sb, agent)
	project := domain.ProjectRecord{ID: "ws", Path: "/repos/ws", Kind: domain.ProjectKindWorkspace, Config: sandboxedConfig(domain.SandboxModePerAgent)}
	rec := domain.SessionRecord{ID: "ws-1", ProjectID: "ws", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode}

	launch, err := m.prepareSandboxLaunch(ctx, agent, rec, project, "/d/worktrees/ws/ws-1", map[string]string{})
	if !errors.Is(err, ports.ErrSandboxMisconfigured) || !strings.Contains(err.Error(), "single-repository projects only") {
		t.Fatalf("err = %v, want ErrSandboxMisconfigured about single-repository projects", err)
	}
	if launch != nil || len(sb.ensured) != 0 {
		t.Fatalf("launch=%v ensured=%d, want no container", launch, len(sb.ensured))
	}
	if m.sandboxInFlight.active("ws") {
		t.Fatal("refused launch left an in-flight registration")
	}
}

func TestSpawnSandboxRejectsHarnessWithoutSandboxSupport(t *testing.T) {
	sb := &fakeSandbox{}
	m, _, rt := newSandboxSpawnManager(sb, launchArgvAgent{argv: []string{"codex"}})

	_, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex})
	if !errors.Is(err, ports.ErrSandboxMisconfigured) {
		t.Fatalf("spawn err = %v, want ErrSandboxMisconfigured", err)
	}
	if rt.created != 0 || len(sb.ensured) != 0 {
		t.Fatalf("created=%d ensured=%d, want neither", rt.created, len(sb.ensured))
	}
}

func TestSpawnSandboxRejectsExplicitChatBeforeCreatingSession(t *testing.T) {
	sb := &fakeSandbox{}
	agent := &sandboxAgent{launchArgvAgent: launchArgvAgent{argv: []string{"claude"}}}
	m, st, rt := newSandboxSpawnManager(sb, agent)

	_, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, RequestedMode: domain.SessionModeChat})
	if !errors.Is(err, ports.ErrSandboxMisconfigured) {
		t.Fatalf("spawn err = %v, want ErrSandboxMisconfigured", err)
	}
	if len(st.sessions) != 0 || rt.created != 0 || len(sb.ensured) != 0 {
		t.Fatalf("chat rejection had side effects: sessions=%d created=%d ensured=%d", len(st.sessions), rt.created, len(sb.ensured))
	}
}

func TestSpawnSandboxedOrchestratorDefaultChatFallsBackToTerminal(t *testing.T) {
	sb := &fakeSandbox{}
	agent := &sandboxAgent{launchArgvAgent: launchArgvAgent{argv: []string{"claude"}}}
	m, st, rt := newSandboxSpawnManager(sb, agent)
	// The orchestrator is sandboxed only by an explicit override.
	cfg := st.projects["mer"].Config
	cfg.Orchestrator.Sandbox = &domain.SandboxConfig{Mode: domain.SandboxModePerAgent, Image: "node:22"}
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: cfg}

	rec, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindOrchestrator, Harness: domain.HarnessClaudeCode})
	if err != nil {
		t.Fatal(err)
	}
	if domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeTUI || len(sb.ensured) != 1 || rt.created != 1 {
		t.Fatalf("mode=%q ensured=%d created=%d, want a sandboxed terminal launch", rec.Mode, len(sb.ensured), rt.created)
	}
}

func TestSpawnUnsandboxedRoleIgnoresSandbox(t *testing.T) {
	sb := &fakeSandbox{}
	agent := supervisedLaunchAgent{launchArgvAgent{argv: []string{"codex"}}}
	m, st, rt := newSandboxSpawnManager(sb, agent)
	cfg := st.projects["mer"].Config
	cfg.Worker.Sandbox = &domain.SandboxConfig{Mode: domain.SandboxModeOff}
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: cfg}

	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex}); err != nil {
		t.Fatal(err)
	}
	want := []string{"/opt/ao", "agent-process", "supervise", "--session", "mer-1", "--launch", "launch-7", "--", "codex"}
	if len(sb.ensured) != 0 || !reflect.DeepEqual(rt.lastCfg.Argv, want) {
		t.Fatalf("ensured=%d argv=%#v, want host launch %#v", len(sb.ensured), rt.lastCfg.Argv, want)
	}
}

func TestRestoreSandboxedRelaunchesThroughDockerExec(t *testing.T) {
	sb := &fakeSandbox{}
	agent := &sandboxAgent{launchArgvAgent: launchArgvAgent{argv: []string{"/host/claude", "--resume", "agent-x"}}}
	m, st, rt := newSandboxSpawnManager(sb, agent)
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", AgentSessionID: "agent-x", RuntimeLaunchID: "launch-old"})
	rec := st.sessions["mer-1"]
	rec.Harness = domain.HarnessClaudeCode
	st.sessions["mer-1"] = rec

	if _, err := m.RestoreWithMode(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if len(sb.ensured) != 1 || sb.ensured[0].WorkspacePath != "/ws/mer-1" {
		t.Fatalf("ensure requests = %+v", sb.ensured)
	}
	want := []string{"/opt/ao", "agent-process", "supervise", "--session", "mer-1", "--launch", "launch-7",
		"--sandbox-relay", "/d/s/relay", "--",
		"/usr/bin/docker", "exec", "-i", "-t", "-w", "/ws/mer-1", "ao-sbx-mer-1", "/bin/sh", "/d/sandbox/bin/ao-enter", "/d/s/env",
		"claude", "--resume", "agent-x"}
	if !reflect.DeepEqual(rt.lastCfg.Argv, want) {
		t.Fatalf("restored argv =\n%#v\nwant\n%#v", rt.lastCfg.Argv, want)
	}
}

func TestRestoreSandboxUnavailableDoesNotLaunchOnHost(t *testing.T) {
	sb := &fakeSandbox{ensureErr: fmt.Errorf("%w: docker down", ports.ErrSandboxUnavailable)}
	agent := &sandboxAgent{launchArgvAgent: launchArgvAgent{argv: []string{"claude"}}}
	m, st, rt := newSandboxSpawnManager(sb, agent)
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", AgentSessionID: "agent-x"})

	_, err := m.RestoreWithMode(ctx, "mer-1")
	if !errors.Is(err, ports.ErrSandboxUnavailable) {
		t.Fatalf("restore err = %v, want ErrSandboxUnavailable", err)
	}
	if rt.created != 0 {
		t.Fatalf("runtime Create calls = %d, want 0", rt.created)
	}
}

func TestKillReleasesSharedSandboxOnlyWhenUnused(t *testing.T) {
	m, st, _, _ := newManager()
	sb := &fakeSandbox{}
	m.sandbox = sb
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: sandboxedConfig(domain.SandboxModeShared)}
	st.sessions["mer-1"] = mkLive("mer-1")
	st.sessions["mer-2"] = mkLive("mer-2")

	if _, err := m.Kill(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if !sameReleases(sb.released, []releaseCall{{inUse: true}}) {
		t.Fatalf("after first kill released = %+v, want the container kept for mer-2", sb.released)
	}
	if _, err := m.Kill(ctx, "mer-2"); err != nil {
		t.Fatal(err)
	}
	if !sameReleases(sb.released, []releaseCall{{inUse: true}, {inUse: false}}) {
		t.Fatalf("after last kill released = %+v, want unused only after mer-2 terminated", sb.released)
	}
}

func TestKillKeepsSharedSandboxWhenSessionListFails(t *testing.T) {
	m, st, _, _ := newManager()
	sb := &fakeSandbox{}
	m.sandbox = sb
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: sandboxedConfig(domain.SandboxModeShared)}
	st.sessions["mer-1"] = mkLive("mer-1")
	st.listSessionsErr = errors.New("db locked")

	if _, err := m.Kill(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if len(sb.released) != 1 || sb.released[0].err == nil {
		t.Fatalf("released = %+v, want one call whose inUse reported the list error", sb.released)
	}
}

func TestSwitchAgentRejectsSandboxedSessionBeforeCreatingSaga(t *testing.T) {
	runtime := &fakeRestartRuntime{fakeRuntime: &fakeRuntime{}}
	manager, store, _ := newSwitchTestManager(t, runtime)
	project := store.projects["proj"]
	project.Config.Sandbox = domain.SandboxConfig{Mode: domain.SandboxModePerAgent, Image: "node:22"}
	store.projects["proj"] = project

	_, err := switchAgentSynchronously(context.Background(), manager, "proj-1", SwitchAgentConfig{TargetHarness: domain.HarnessCodex, IdempotencyKey: "sandboxed"})
	if !errors.Is(err, ports.ErrSandboxMisconfigured) {
		t.Fatalf("switch error = %v, want ErrSandboxMisconfigured", err)
	}
	if len(store.switches) != 0 || runtime.restarted != 0 || runtime.destroyed != 0 {
		t.Fatalf("sandbox rejection had side effects: switches=%d restarts=%d destroys=%d", len(store.switches), runtime.restarted, runtime.destroyed)
	}
}

func TestInterfaceTransitionToChatRejectedForSandboxedSession(t *testing.T) {
	m, st, _, _ := newManager()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: sandboxedConfig(domain.SandboxModePerAgent)}
	rec := mkLive("mer-1")
	rec.Kind = domain.KindWorker
	err := m.preflightInterfaceTarget(ctx, rec, domain.SessionInterfaceTransition{SessionID: "mer-1", TargetMode: domain.SessionModeChat})
	if !errors.Is(err, ports.ErrSandboxMisconfigured) {
		t.Fatalf("preflight err = %v, want ErrSandboxMisconfigured", err)
	}
}

func TestSpawnDiscardsContainerWhenPrepareSandboxFails(t *testing.T) {
	sb := &fakeSandbox{}
	agent := &sandboxAgent{launchArgvAgent: launchArgvAgent{argv: []string{"claude"}}, prepareErr: errors.New("no credentials")}
	m, _, rt := newSandboxSpawnManager(sb, agent)

	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode}); err == nil {
		t.Fatal("spawn succeeded, want the PrepareSandbox error")
	}
	if len(sb.discarded) != 1 || sb.discarded[0].Container != "ao-sbx-mer-1" || rt.created != 0 {
		t.Fatalf("discarded=%+v created=%d, want exactly one discard and no launch", sb.discarded, rt.created)
	}
	if m.sandboxInFlight.active("mer") {
		t.Fatal("failed spawn left a launch in flight")
	}
}

func TestSpawnDiscardsContainerWhenRuntimeCreateFails(t *testing.T) {
	sb := &fakeSandbox{}
	agent := &sandboxAgent{launchArgvAgent: launchArgvAgent{argv: []string{"claude"}}}
	m, _, rt := newSandboxSpawnManager(sb, agent)
	rt.createErr = errors.New("tmux exploded")

	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode}); err == nil {
		t.Fatal("spawn succeeded, want the runtime error")
	}
	if len(sb.discarded) != 1 || sb.discarded[0].Mode != domain.SandboxModePerAgent {
		t.Fatalf("discarded=%+v, want exactly one per-agent discard", sb.discarded)
	}
	if m.sandboxInFlight.active("mer") {
		t.Fatal("failed spawn left a launch in flight")
	}
}

func TestReleaseSharedSandboxKeepsContainerWhileLaunchInFlight(t *testing.T) {
	m, st, _, _ := newManager()
	sb := &fakeSandbox{}
	m.sandbox = sb
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1"})

	done := m.sandboxInFlight.begin("mer")
	m.releaseSharedSandbox(ctx, "mer")
	done()
	m.releaseSharedSandbox(ctx, "mer")
	if !sameReleases(sb.released, []releaseCall{{inUse: true}, {inUse: false}}) {
		t.Fatalf("released = %+v, want in use only while the launch is in flight", sb.released)
	}
}

func TestRestoreHoldsInFlightUntilLaunched(t *testing.T) {
	sb := &fakeSandbox{}
	agent := &sandboxAgent{launchArgvAgent: launchArgvAgent{argv: []string{"claude"}}}
	m, st, rt := newSandboxSpawnManager(sb, agent)
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", AgentSessionID: "agent-x"})
	var inFlightAtCreate bool
	rt.onCreate = func() { inFlightAtCreate = m.sandboxInFlight.active("mer") }

	if _, err := m.RestoreWithMode(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if !inFlightAtCreate || m.sandboxInFlight.active("mer") {
		t.Fatalf("inFlight at create=%v after=%v, want held through launch and cleared after", inFlightAtCreate, m.sandboxInFlight.active("mer"))
	}
}

func TestRestoreChatSessionRefusedForSandboxedRole(t *testing.T) {
	launcher := &recordingLauncher{}
	m, st, _ := newChatManager(launcher)
	rec, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: chatTestProject, Kind: domain.KindWorker, Harness: domain.HarnessCodex, RequestedMode: domain.SessionModeChat})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Kill(ctx, rec.ID); err != nil {
		t.Fatal(err)
	}
	// The sandbox is enabled after the chat session exists.
	project := st.projects[string(chatTestProject)]
	project.Config.Sandbox = domain.SandboxConfig{Mode: domain.SandboxModePerAgent, Image: "node:22"}
	st.projects[string(chatTestProject)] = project
	starts := len(launcher.started)

	_, err = m.RestoreWithMode(ctx, rec.ID)
	if !errors.Is(err, ports.ErrSandboxMisconfigured) {
		t.Fatalf("restore err = %v, want ErrSandboxMisconfigured", err)
	}
	if len(launcher.started) != starts {
		t.Fatalf("chat controller started %d times on restore, want 0", len(launcher.started)-starts)
	}
}

func TestKillFailingBeforeTerminationKeepsSharedSandbox(t *testing.T) {
	m, st, rt, _ := newManager()
	sb := &fakeSandbox{}
	m.sandbox = sb
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: sandboxedConfig(domain.SandboxModeShared)}
	st.sessions["mer-1"] = mkLive("mer-1")
	rt.destroyErr = errors.New("runtime still alive")

	if _, err := m.Kill(ctx, "mer-1"); err == nil {
		t.Fatal("kill succeeded, want the runtime error")
	}
	if !sameReleases(sb.released, []releaseCall{{inUse: true}}) {
		t.Fatalf("released = %+v, want the container kept for the still-live session", sb.released)
	}
}

func TestInterfaceTransitionToTerminalSkipsHostBinaryCheckForSandboxedRole(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: sandboxedConfig(domain.SandboxModePerAgent)}
	agent := &sandboxAgent{launchArgvAgent: launchArgvAgent{argv: []string{"/missing/on/host/claude"}}}
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st,
		Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, Sandbox: &fakeSandbox{},
		LookPath: func(string) (string, error) { return "", errors.New("not found") },
	})
	rec := mkLive("mer-1")
	rec.Kind = domain.KindWorker
	rec.Harness = domain.HarnessClaudeCode
	err := m.preflightInterfaceTarget(ctx, rec, domain.SessionInterfaceTransition{SessionID: "mer-1", TargetMode: domain.SessionModeTUI})
	if err != nil {
		t.Fatalf("preflight err = %v, want nil: the harness lives in the image", err)
	}
}

// hostlessSandboxAgent models Claude installed only in the image: building a
// host launch fails, a sandboxed one uses the bare in-image name.
type hostlessSandboxAgent struct{ sandboxAgent }

func (a *hostlessSandboxAgent) GetLaunchCommand(_ context.Context, cfg ports.LaunchConfig) ([]string, error) {
	if !cfg.Sandboxed {
		return nil, fmt.Errorf("%w: claude", ports.ErrAgentBinaryNotFound)
	}
	return []string{"claude", "--x"}, nil
}

func (a *hostlessSandboxAgent) GetRestoreCommand(_ context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	if !cfg.Sandboxed {
		return nil, false, fmt.Errorf("%w: claude", ports.ErrAgentBinaryNotFound)
	}
	return []string{"claude", "--resume", "agent-x"}, true, nil
}

func newHostlessSandboxManager(sb *fakeSandbox) (*Manager, *fakeStore, *fakeRuntime) {
	m, st, rt := newSandboxSpawnManager(sb, &hostlessSandboxAgent{})
	m.lookPath = func(name string) (string, error) {
		if filepath.Base(name) == "claude" {
			return "", errors.New("claude is not on the host PATH")
		}
		return "/bin/true", nil
	}
	return m, st, rt
}

// I1: a sandboxed role never needs the harness on the host.
func TestSpawnSandboxedSucceedsWithoutHostBinary(t *testing.T) {
	sb := &fakeSandbox{}
	m, _, rt := newHostlessSandboxManager(sb)
	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode}); err != nil {
		t.Fatal(err)
	}
	argv := rt.lastCfg.Argv
	if got := argv[len(argv)-2:]; !reflect.DeepEqual(got, []string{"claude", "--x"}) {
		t.Fatalf("argv = %q, want the in-image claude after the docker exec wrap", argv)
	}
}

func TestRestoreSandboxedSucceedsWithoutHostBinary(t *testing.T) {
	sb := &fakeSandbox{}
	m, st, rt := newHostlessSandboxManager(sb)
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", AgentSessionID: "agent-x"})
	rec := st.sessions["mer-1"]
	rec.Harness = domain.HarnessClaudeCode
	st.sessions["mer-1"] = rec
	if _, err := m.RestoreWithMode(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	argv := rt.lastCfg.Argv
	if got := argv[len(argv)-3:]; !reflect.DeepEqual(got, []string{"claude", "--resume", "agent-x"}) {
		t.Fatalf("argv = %q, want the in-image claude after the docker exec wrap", argv)
	}
}

// I2: kill removes a per-agent container itself, independent of the
// container reaper opt-out, but only once termination is recorded.
func TestKillDiscardsPerAgentSandboxAfterTermination(t *testing.T) {
	m, st, _, _ := newManager()
	sb := &fakeSandbox{}
	m.sandbox = sb
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: sandboxedConfig(domain.SandboxModePerAgent)}
	st.sessions["mer-1"] = mkLive("mer-1")
	if _, err := m.Kill(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sb.sessionDiscards, []string{"mer/mer-1"}) {
		t.Fatalf("session discards = %v, want the killed session's container", sb.sessionDiscards)
	}
}

func TestKillFailingBeforeTerminationKeepsPerAgentSandbox(t *testing.T) {
	m, st, rt, _ := newManager()
	sb := &fakeSandbox{}
	m.sandbox = sb
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: sandboxedConfig(domain.SandboxModePerAgent)}
	st.sessions["mer-1"] = mkLive("mer-1")
	rt.destroyErr = errors.New("runtime still alive")
	if _, err := m.Kill(ctx, "mer-1"); err == nil {
		t.Fatal("kill succeeded, want the runtime error")
	}
	if len(sb.sessionDiscards) != 0 {
		t.Fatalf("session discards = %v, want none for a possibly-live agent", sb.sessionDiscards)
	}
}

// A per-agent session killed after the project turned sandboxing off still
// has its container removed: the decision follows the session's state.
func TestKillDiscardsSandboxAfterModeChange(t *testing.T) {
	m, st, _, _ := newManager()
	sb := &fakeSandbox{}
	m.sandbox = sb
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	st.sessions["mer-1"] = mkLive("mer-1")
	if _, err := m.Kill(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sb.sessionDiscards, []string{"mer/mer-1"}) {
		t.Fatalf("session discards = %v, want the killed session's container", sb.sessionDiscards)
	}
}

// Killing a session that never ran sandboxed touches no sandbox, even in a
// project that sandboxes now.
func TestKillWithoutSandboxStateSkipsSandbox(t *testing.T) {
	m, st, _, _ := newManager()
	sb := &fakeSandbox{noState: map[domain.SessionID]bool{"mer-1": true}}
	m.sandbox = sb
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: sandboxedConfig(domain.SandboxModeShared)}
	st.sessions["mer-1"] = mkLive("mer-1")
	if _, err := m.Kill(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if len(sb.sessionDiscards) != 0 || len(sb.released) != 0 {
		t.Fatalf("discards = %v, released = %+v, want no sandbox calls", sb.sessionDiscards, sb.released)
	}
}

// A live host-only session, such as the unsandboxed orchestrator, does not
// keep the shared container alive.
func TestReleaseSharedSandboxIgnoresLiveHostSessions(t *testing.T) {
	m, st, _, _ := newManager()
	sb := &fakeSandbox{noState: map[domain.SessionID]bool{"mer-orch": true}}
	m.sandbox = sb
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: sandboxedConfig(domain.SandboxModeShared)}
	st.sessions["mer-orch"] = mkLive("mer-orch")
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1"})

	m.ReleaseTerminatedSandbox(ctx, "mer-1")
	if !sameReleases(sb.released, []releaseCall{{inUse: false}}) {
		t.Fatalf("released = %+v, want the container released despite the live host session", sb.released)
	}
}

// Every terminal path, not only kill, releases the shared container; a
// session without sandbox state does not reach Docker at all.
func TestReleaseTerminatedSandboxOnlyForSandboxedSessions(t *testing.T) {
	m, st, _, _ := newManager()
	sb := &fakeSandbox{noState: map[domain.SessionID]bool{"mer-2": true}}
	m.sandbox = sb
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1"})
	seedTerminal(st, "mer-2", domain.SessionMetadata{WorkspacePath: "/ws/mer-2"})

	m.ReleaseTerminatedSandbox(ctx, "mer-2")
	if len(sb.released) != 0 {
		t.Fatalf("released = %+v, want none for a host session", sb.released)
	}
	m.ReleaseTerminatedSandbox(ctx, "mer-1")
	if !sameReleases(sb.released, []releaseCall{{inUse: false}}) {
		t.Fatalf("released = %+v, want one release", sb.released)
	}
}

// A conversation is never resumed across a sandbox mode change: the harness
// would not find it and would silently start over.
func TestRestoreRefusesAcrossSandboxModeChange(t *testing.T) {
	for _, tc := range []struct {
		name        string
		sandboxedAt bool // whether the session has sandbox state
		cfg         domain.ProjectConfig
	}{
		{"host to sandbox", false, sandboxedConfig(domain.SandboxModePerAgent)},
		{"sandbox to host", true, testRoleAgents()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sb := &fakeSandbox{noState: map[domain.SessionID]bool{"mer-1": !tc.sandboxedAt}}
			agent := &sandboxAgent{launchArgvAgent: launchArgvAgent{argv: []string{"claude"}}}
			m, st, rt := newSandboxSpawnManager(sb, agent)
			st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repos/mer", Config: tc.cfg}
			seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", AgentSessionID: "agent-x"})

			_, err := m.RestoreWithMode(ctx, "mer-1")
			if !errors.Is(err, ports.ErrSandboxMisconfigured) || !strings.Contains(err.Error(), "sandbox.mode back") {
				t.Fatalf("restore err = %v, want a sandbox mode change refusal", err)
			}
			if rt.created != 0 || len(sb.ensured) != 0 {
				t.Fatalf("runtime creates = %d, ensures = %d, want none", rt.created, len(sb.ensured))
			}
		})
	}
}

// Without a native conversation there is nothing to lose, so the session
// follows the current mode.
func TestRestoreWithoutConversationFollowsNewSandboxMode(t *testing.T) {
	sb := &fakeSandbox{noState: map[domain.SessionID]bool{"mer-1": true}}
	agent := &sandboxAgent{launchArgvAgent: launchArgvAgent{argv: []string{"claude"}}}
	m, st, _ := newSandboxSpawnManager(sb, agent)
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", Prompt: "do it"})

	if _, err := m.RestoreWithMode(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if len(sb.ensured) != 1 {
		t.Fatalf("ensures = %d, want the restore to run sandboxed", len(sb.ensured))
	}
}

// I2: a restore that fails after Ensure removes the container it ensured.
func TestRestoreDiscardsContainerWhenRuntimeCreateFails(t *testing.T) {
	sb := &fakeSandbox{}
	agent := &sandboxAgent{launchArgvAgent: launchArgvAgent{argv: []string{"claude"}}}
	m, st, rt := newSandboxSpawnManager(sb, agent)
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", AgentSessionID: "agent-x"})
	rt.createErr = errors.New("tmux exploded")
	if _, err := m.RestoreWithMode(ctx, "mer-1"); err == nil {
		t.Fatal("restore succeeded, want the runtime error")
	}
	if len(sb.discarded) != 1 || sb.discarded[0].Container != "ao-sbx-mer-1" {
		t.Fatalf("discarded = %+v, want the restored session's container", sb.discarded)
	}
	if m.sandboxInFlight.active("mer") {
		t.Fatal("failed restore left a launch in flight")
	}
}

func TestRestoreSuccessKeepsContainer(t *testing.T) {
	sb := &fakeSandbox{}
	agent := &sandboxAgent{launchArgvAgent: launchArgvAgent{argv: []string{"claude"}}}
	m, st, _ := newSandboxSpawnManager(sb, agent)
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", AgentSessionID: "agent-x"})
	if _, err := m.RestoreWithMode(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if len(sb.discarded) != 0 {
		t.Fatalf("discarded = %+v, want the live session's container kept", sb.discarded)
	}
}

// I4: the project block applies to workers; the orchestrator runs on the
// host unless orchestrator.sandbox is set explicitly.
func TestSandboxForOrchestratorNeedsExplicitOverride(t *testing.T) {
	cfg := sandboxedConfig(domain.SandboxModePerAgent)
	if sandboxFor(domain.KindOrchestrator, cfg).Enabled() {
		t.Fatal("orchestrator inherited the project sandbox block")
	}
	if !sandboxFor(domain.KindWorker, cfg).Enabled() {
		t.Fatal("worker did not inherit the project sandbox block")
	}
	cfg.Orchestrator.Sandbox = &domain.SandboxConfig{Mode: domain.SandboxModeShared, Image: "node:22"}
	if got := sandboxFor(domain.KindOrchestrator, cfg); got.Mode != domain.SandboxModeShared {
		t.Fatalf("explicit orchestrator sandbox = %+v, want shared", got)
	}
}

func TestSpawnOrchestratorIgnoresProjectSandbox(t *testing.T) {
	sb := &fakeSandbox{}
	agent := supervisedLaunchAgent{launchArgvAgent{argv: []string{"claude"}}}
	m, _, rt := newSandboxSpawnManager(sb, agent)
	if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindOrchestrator, Harness: domain.HarnessClaudeCode, RequestedMode: domain.SessionModeTUI}); err != nil {
		t.Fatal(err)
	}
	if len(sb.ensured) != 0 || rt.created != 1 {
		t.Fatalf("ensured=%d created=%d, want a host orchestrator launch", len(sb.ensured), rt.created)
	}
}
