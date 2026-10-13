package dockersandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeCall struct {
	match string // substring the joined argv must contain
	out   string
	err   error
}

type fakeRunner struct {
	t      *testing.T
	script []fakeCall
	calls  []string
}

func (f *fakeRunner) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	line := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, line)
	if len(f.script) == 0 {
		f.t.Fatalf("unexpected call: %s", line)
	}
	c := f.script[0]
	f.script = f.script[1:]
	if !strings.Contains(line, c.match) {
		f.t.Fatalf("call %q does not contain %q", line, c.match)
	}
	return []byte(c.out), c.err
}

func testSandbox(t *testing.T, script []fakeCall) (*Sandbox, *fakeRunner, string) {
	t.Helper()
	data := t.TempDir()
	fr := &fakeRunner{t: t, script: script}
	return newWithRunner(Config{DataDir: data, ManagedRoot: filepath.Join(data, "worktrees")}, fr, "/usr/bin/docker"), fr, data
}

func perAgentReq(ws string) ports.SandboxRequest {
	return ports.SandboxRequest{SessionID: "s1", ProjectID: "p", WorkspacePath: ws, RequireBinaries: []string{"claude"},
		Config: domain.SandboxConfig{Mode: domain.SandboxModePerAgent, Image: "node:22"}}
}

func TestEnsureCreatesPerAgentContainer(t *testing.T) {
	s, fr, data := testSandbox(t, []fakeCall{
		{match: "rev-parse --path-format=absolute --git-common-dir", out: "/repo/.git\n"},
		{match: "inspect", err: errors.New("Error: No such object: ao-sbx-s1")},
		{match: "run -d --init --name ao-sbx-s1"},
		{match: "exec ao-sbx-s1 /bin/sh -c command -v 'claude'"},
	})
	ws := filepath.Join(data, "worktrees", "p", "s1")
	target, err := s.Ensure(context.Background(), perAgentReq(ws))
	if err != nil {
		t.Fatal(err)
	}
	run := fr.calls[2]
	for _, want := range []string{"--label ao.session=s1", "-v " + ws + ":" + ws, "-v /repo/.git:/repo/.git",
		filepath.Join(data, "prompts") + ":ro", filepath.Join(data, "sandbox", "bin") + ":ro"} {
		if !strings.Contains(run, want) {
			t.Fatalf("docker run missing %q:\n%s", want, run)
		}
	}
	if target.Container != "ao-sbx-s1" || target.SessionDir != filepath.Join(data, "sandbox", "projects", "p", "sessions", "s1") {
		t.Fatalf("target = %#v", target)
	}
	if _, err := os.Stat(filepath.Join(target.BinDir, "ao-enter")); err != nil {
		t.Fatalf("scripts not installed: %v", err)
	}
}

func TestEnsureReusesRunningContainerWithSameHash(t *testing.T) {
	s, _, data := testSandbox(t, nil)
	ws := filepath.Join(data, "worktrees", "p", "s1")
	hash := s.hashFor(perAgentReq(ws), "/repo/.git")
	s.run.(*fakeRunner).script = []fakeCall{
		{match: "rev-parse", out: "/repo/.git\n"},
		{match: "inspect", out: "running\t" + hash + "\n"},
		{match: "command -v 'claude'"},
	}
	if _, err := s.Ensure(context.Background(), perAgentReq(ws)); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureStartsStoppedContainer(t *testing.T) {
	s, _, data := testSandbox(t, nil)
	ws := filepath.Join(data, "worktrees", "p", "s1")
	hash := s.hashFor(perAgentReq(ws), "/repo/.git")
	s.run.(*fakeRunner).script = []fakeCall{
		{match: "rev-parse", out: "/repo/.git\n"},
		{match: "inspect", out: "exited\t" + hash + "\n"},
		{match: "start ao-sbx-s1"},
		{match: "command -v 'claude'"},
	}
	if _, err := s.Ensure(context.Background(), perAgentReq(ws)); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureDockerUnavailable(t *testing.T) {
	s, _, data := testSandbox(t, []fakeCall{
		{match: "rev-parse", out: "/repo/.git\n"},
		{match: "inspect", err: errors.New("Cannot connect to the Docker daemon at unix:///var/run/docker.sock")},
	})
	_, err := s.Ensure(context.Background(), perAgentReq(filepath.Join(data, "worktrees", "p", "s1")))
	if !errors.Is(err, ports.ErrSandboxUnavailable) {
		t.Fatalf("err = %v, want ErrSandboxUnavailable", err)
	}
}

func TestEnsureMissingHarnessBinary(t *testing.T) {
	s, _, data := testSandbox(t, []fakeCall{
		{match: "rev-parse", out: "/repo/.git\n"},
		{match: "inspect", err: errors.New("No such object")},
		{match: "run -d"},
		{match: "command -v 'claude'", err: errors.New("exit status 1")},
	})
	_, err := s.Ensure(context.Background(), perAgentReq(filepath.Join(data, "worktrees", "p", "s1")))
	if !errors.Is(err, ports.ErrSandboxMisconfigured) || !strings.Contains(err.Error(), `node:22`) || !strings.Contains(err.Error(), `"claude"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestEnsureSharedRejectsWorkspaceOutsideManagedRoot(t *testing.T) {
	s, _, _ := testSandbox(t, []fakeCall{{match: "rev-parse", out: "/repo/.git\n"}})
	req := perAgentReq("/elsewhere/s1")
	req.Config.Mode = domain.SandboxModeShared
	if _, err := s.Ensure(context.Background(), req); !errors.Is(err, ports.ErrSandboxMisconfigured) {
		t.Fatalf("err = %v, want ErrSandboxMisconfigured", err)
	}
}

func TestEnsureSharedHasNoSessionLabel(t *testing.T) {
	s, fr, data := testSandbox(t, nil)
	ws := filepath.Join(data, "worktrees", "p", "s1")
	req := perAgentReq(ws)
	req.Config.Mode = domain.SandboxModeShared
	fr.script = []fakeCall{
		{match: "rev-parse", out: "/repo/.git\n"},
		{match: "inspect", err: errors.New("No such object")},
		{match: "run -d"},
		{match: "command -v"},
	}
	if _, err := s.Ensure(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	run := fr.calls[2]
	if strings.Contains(run, "ao.session=") || !strings.Contains(run, "-v "+filepath.Join(data, "worktrees", "p")+":") {
		t.Fatalf("shared run args wrong:\n%s", run)
	}
}

func TestExecDoesNotPutEnvInArgv(t *testing.T) {
	s, _, data := testSandbox(t, nil)
	target := s.targetFor("ao-sbx-s1", "p", "s1")
	if err := os.MkdirAll(target.SessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	argv, err := s.Exec(target, filepath.Join(data, "w"), map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "secret"}, []string{"claude"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(argv, " "), "secret") {
		t.Fatalf("secret in argv: %q", argv)
	}
	info, err := os.Stat(filepath.Join(target.SessionDir, "env"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("env file = %v, %v; want 0600", info, err)
	}
	body, _ := os.ReadFile(filepath.Join(target.SessionDir, "env"))
	for _, want := range []string{"HOME='" + filepath.Join(target.ProjectDir, "home") + "'", "AO_SANDBOX_RELAY='" + target.RelayDir + "'"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("env file missing %q:\n%s", want, body)
		}
	}
}

func TestReleaseRemovesSharedContainersOnlyWhenUnused(t *testing.T) {
	s, fr, _ := testSandbox(t, nil)
	if err := s.Release(context.Background(), "p", true); err != nil || len(fr.calls) != 0 {
		t.Fatalf("in-use release = %v, calls %v", err, fr.calls)
	}
	fr.script = []fakeCall{
		{match: "ps -a --filter label=ao.project=p --filter label=ao.sandbox=shared", out: "c1\nc2\n"},
		{match: "rm -f c1"},
		{match: "rm -f c2"},
	}
	if err := s.Release(context.Background(), "p", false); err != nil {
		t.Fatal(err)
	}
}
