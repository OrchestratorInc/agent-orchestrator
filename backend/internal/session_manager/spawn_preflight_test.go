package sessionmanager

import (
	"errors"
	"fmt"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/gitdefault"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestRuntimePrerequisiteErrorPerOS(t *testing.T) {
	t.Setenv("AO_TMUX_BINARY", "")
	noExecutable := func() (string, error) { return "", errors.New("no executable") }
	noTmux := func(name string) (string, error) { return "", fmt.Errorf("exec: %q: not found", name) }
	withTmux := func(name string) (string, error) { return "/usr/local/bin/tmux", nil }
	cases := []struct {
		goos     string
		lookPath func(string) (string, error)
		wantErr  bool
	}{
		{goos: "windows", lookPath: noTmux},
		{goos: "linux", lookPath: noTmux},
		{goos: "darwin", lookPath: noTmux, wantErr: true},
		{goos: "darwin", lookPath: withTmux},
	}
	for _, tc := range cases {
		err := RuntimePrerequisiteError(tc.goos, noExecutable, tc.lookPath)
		if tc.wantErr != (err != nil) {
			t.Fatalf("%s: err = %v, wantErr %v", tc.goos, err, tc.wantErr)
		}
		if err != nil && !errors.Is(err, ports.ErrRuntimePrerequisite) {
			t.Fatalf("%s: err = %v, want ports.ErrRuntimePrerequisite", tc.goos, err)
		}
	}
}

func TestPreflightSpawnResolvesRoleHarnessAndUnresolvedDefaultBranch(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repo/mer", Config: testRoleAgents()}
	ws := &fakeWorkspace{resolveErr: map[string]error{
		"/repo/mer": fmt.Errorf("gitworktree: resolve repository default: %w", gitdefault.ErrUnresolved),
	}}
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: fakeAgents{}, Workspace: ws, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}})

	facts, err := m.PreflightSpawn(ctx, SpawnPreflightInput{ProjectID: "mer", Kind: domain.KindWorker, Mode: domain.SessionModeTUI})
	if err != nil {
		t.Fatalf("PreflightSpawn: %v", err)
	}
	if facts.Harness != domain.HarnessClaudeCode || facts.Mode != domain.SessionModeTUI || !facts.DefaultBranchUnresolved {
		t.Fatalf("facts = %+v, want claude-code, tui, unresolved branch", facts)
	}
	if len(ws.resolves) != 1 || ws.resolves[0].repoPath != "/repo/mer" {
		t.Fatalf("resolves = %+v, want one root resolution", ws.resolves)
	}
}

func TestPreflightSpawnPassesResolvedBranchAndExplicitHarness(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repo/mer"}
	ws := &fakeWorkspace{}
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: fakeAgents{}, Workspace: ws, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}})

	facts, err := m.PreflightSpawn(ctx, SpawnPreflightInput{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeTUI})
	if err != nil {
		t.Fatalf("PreflightSpawn: %v", err)
	}
	if facts.Harness != domain.HarnessCodex || facts.DefaultBranchUnresolved {
		t.Fatalf("facts = %+v, want codex with a resolved branch", facts)
	}
}

func TestPreflightSpawnWithoutHarnessOrProject(t *testing.T) {
	st := newFakeStore()
	st.projects["bare"] = domain.ProjectRecord{ID: "bare", Path: "/repo/bare"}
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: fakeAgents{}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}})

	facts, err := m.PreflightSpawn(ctx, SpawnPreflightInput{ProjectID: "bare", Kind: domain.KindOrchestrator})
	if err != nil || facts.Harness != "" {
		t.Fatalf("facts, err = %+v, %v; want no harness and no error", facts, err)
	}
	if _, err := m.PreflightSpawn(ctx, SpawnPreflightInput{ProjectID: "missing", Kind: domain.KindWorker}); !errors.Is(err, ErrProjectNotResolvable) {
		t.Fatalf("err = %v, want ErrProjectNotResolvable", err)
	}
}
