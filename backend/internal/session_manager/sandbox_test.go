package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type releaseCall struct {
	inUse bool
	err   error
}

type fakeSandbox struct {
	ensureErr error
	ensured   []ports.SandboxRequest
	execEnv   map[string]string
	released  []releaseCall
}

func (f *fakeSandbox) Ensure(_ context.Context, req ports.SandboxRequest) (ports.SandboxTarget, error) {
	f.ensured = append(f.ensured, req)
	if f.ensureErr != nil {
		return ports.SandboxTarget{}, f.ensureErr
	}
	return ports.SandboxTarget{Container: "ao-sbx-" + string(req.SessionID), Docker: "/usr/bin/docker",
		BinDir: "/d/sandbox/bin", ProjectDir: "/d/sandbox/projects/p", SessionDir: "/d/s", RelayDir: "/d/s/relay"}, nil
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
	prepared []string // projectDir, workspacePath per PrepareSandbox call
}

func (*sandboxAgent) SandboxBinary() string { return "claude" }

func (a *sandboxAgent) PrepareSandbox(env map[string]string, _, projectDir, workspacePath string) error {
	a.prepared = append(a.prepared, projectDir, workspacePath)
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
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: sandboxedConfig(domain.SandboxModePerAgent)}
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
	if req.SessionID != "mer-1" || req.ProjectID != "mer" || req.Config.Mode != domain.SandboxModePerAgent ||
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
	m, _, rt := newSandboxSpawnManager(sb, agent)

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
	if !reflect.DeepEqual(sb.released, []releaseCall{{inUse: true}}) {
		t.Fatalf("after first kill released = %+v, want the container kept for mer-2", sb.released)
	}
	if _, err := m.Kill(ctx, "mer-2"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sb.released, []releaseCall{{inUse: true}, {inUse: false}}) {
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
