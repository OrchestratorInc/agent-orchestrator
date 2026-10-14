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

// fakeRunner scripts docker calls in order. Host git calls are answered
// out of band: rev-parse names the fixture git dir, worktreeConfig is off and
// identity keys are unset, unless git overrides a call by substring.
type fakeRunner struct {
	t        *testing.T
	script   []fakeCall
	calls    []string // docker calls
	gitCalls []string
	gitDir   string            // replaces the placeholder fakeGitDir in output
	git      map[string]string // argv substring -> output for git calls
}

// fakeGitDir in fake output stands for the test's real git dir.
const fakeGitDir = "/repo/.git"

func gitDirOf(data string) string { return filepath.Join(data, "repo", ".git") }

// wsOf is the fixture's per-agent workspace, a linked worktree of gitDirOf.
func wsOf(data string) string { return filepath.Join(data, "worktrees", "p", "s1") }

func adminOf(data string) string { return filepath.Join(gitDirOf(data), "worktrees", "s1") }

func (f *fakeRunner) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	line := name + " " + strings.Join(args, " ")
	if name == "git" {
		f.gitCalls = append(f.gitCalls, line)
		return f.gitOutput(line)
	}
	f.calls = append(f.calls, line)
	if len(f.script) == 0 {
		f.t.Fatalf("unexpected call: %s", line)
	}
	c := f.script[0]
	f.script = f.script[1:]
	if !strings.Contains(line, c.match) {
		f.t.Fatalf("call %q does not contain %q", line, c.match)
	}
	return []byte(strings.ReplaceAll(c.out, fakeGitDir, f.gitDir)), c.err
}

func (f *fakeRunner) gitOutput(line string) ([]byte, error) {
	for match, out := range f.git {
		if strings.Contains(line, match) {
			return []byte(strings.ReplaceAll(out, fakeGitDir, f.gitDir)), nil
		}
	}
	switch {
	case strings.Contains(line, "rev-parse --path-format=absolute --git-common-dir"):
		return []byte(f.gitDir + "\n"), nil
	case strings.Contains(line, "extensions.worktreeConfig"):
		return []byte("false\n"), nil
	case strings.Contains(line, "config --get"):
		return nil, errors.New("exit status 1")
	}
	f.t.Fatalf("unexpected git call: %s", line)
	return nil, nil
}

// runCall returns the scripted `docker run` call.
func runCall(t *testing.T, fr *fakeRunner) string {
	t.Helper()
	for _, c := range fr.calls {
		if strings.Contains(c, " run -d ") {
			return c
		}
	}
	t.Fatalf("no docker run in %q", fr.calls)
	return ""
}

func bindArg(p string) string   { return "--mount type=bind,src=" + p + ",dst=" + p + " " }
func roBindArg(p string) string { return "--mount type=bind,src=" + p + ",dst=" + p + ",readonly " }

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// testSandbox builds a data dir holding a repository git dir and a linked
// worktree for session s1, laid out as `git worktree add` would.
func testSandbox(t *testing.T, script []fakeCall) (*Sandbox, *fakeRunner, string) {
	t.Helper()
	data := t.TempDir()
	git := gitDirOf(data)
	mustWrite(t, filepath.Join(git, "config"), "[core]\n")
	for _, d := range []string{"objects", "refs"} {
		if err := os.MkdirAll(filepath.Join(git, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(t, filepath.Join(adminOf(data), "gitdir"), filepath.Join(wsOf(data), ".git")+"\n")
	mustWrite(t, filepath.Join(adminOf(data), "commondir"), "../..\n")
	mustWrite(t, filepath.Join(wsOf(data), ".git"), "gitdir: "+adminOf(data)+"\n")
	fr := &fakeRunner{t: t, script: script, gitDir: git}
	t.Cleanup(func() {
		if len(fr.script) != 0 {
			t.Errorf("unconsumed fake calls: %v", fr.script)
		}
	})
	return newWithRunner(Config{DataDir: data, ManagedRoot: filepath.Join(data, "worktrees")}, fr, "/usr/bin/docker"), fr, data
}

func perAgentReq(ws string) ports.SandboxRequest {
	return ports.SandboxRequest{SessionID: "s1", ProjectID: "p", RepoPath: "/repo", WorkspacePath: ws, RequireBinaries: []string{"claude"},
		Config: domain.SandboxConfig{Mode: domain.SandboxModePerAgent, Image: "node:22"}}
}

// hashFor is the config hash Ensure computes for req.
func hashFor(t *testing.T, s *Sandbox, req ports.SandboxRequest) string {
	t.Helper()
	g, err := s.resolveGit(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.mounts(req, g)
	if err != nil {
		t.Fatal(err)
	}
	return configHash(req.Config, m)
}

func TestEnsureCreatesPerAgentContainer(t *testing.T) {
	s, fr, data := testSandbox(t, []fakeCall{
		{match: "inspect", err: errors.New("Error: No such object: ao-sbx-s1")},
		{match: "run -d --init --name ao-sbx-s1"},
		{match: "exec ao-sbx-s1 /bin/sh -c command -v \"$1\" sh claude"},
	})
	ws := wsOf(data)
	target, err := s.Ensure(context.Background(), perAgentReq(ws))
	if err != nil {
		t.Fatal(err)
	}
	run := runCall(t, fr)
	for _, want := range []string{"--label ao.session=s1", bindArg(ws), roBindArg(gitDirOf(data)),
		roBindArg(filepath.Join(data, "prompts", "s1")), roBindArg(filepath.Join(data, "sandbox", "bin")),
		bindArg(filepath.Join(data, "sandbox", "projects", "p", "home")),
		bindArg(filepath.Join(data, "sandbox", "projects", "p", "claude")),
		bindArg(filepath.Join(data, "sandbox", "projects", "p", "sessions", "s1"))} {
		if !strings.Contains(run+" ", want) {
			t.Fatalf("docker run missing %q:\n%s", want, run)
		}
	}
	if strings.Contains(run, "src="+filepath.Join(data, "sandbox", "projects", "p")+",") {
		t.Fatalf("whole project dir mounted in per-agent mode:\n%s", run)
	}
	if target.Container != "ao-sbx-s1" || target.Mode != domain.SandboxModePerAgent || target.SessionDir != filepath.Join(data, "sandbox", "projects", "p", "sessions", "s1") {
		t.Fatalf("target = %#v", target)
	}
	if _, err := os.Stat(filepath.Join(target.BinDir, "ao-enter")); err != nil {
		t.Fatalf("scripts not installed: %v", err)
	}
}

func TestEnsureReusesRunningContainerWithSameHash(t *testing.T) {
	s, _, data := testSandbox(t, nil)
	ws := wsOf(data)
	hash := hashFor(t, s, perAgentReq(ws))
	s.run.(*fakeRunner).script = []fakeCall{
		{match: "inspect", out: "running\t" + hash + "\n"},
		{match: "command -v \"$1\" sh claude"},
	}
	if _, err := s.Ensure(context.Background(), perAgentReq(ws)); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureStartsStoppedContainer(t *testing.T) {
	s, _, data := testSandbox(t, nil)
	ws := wsOf(data)
	hash := hashFor(t, s, perAgentReq(ws))
	s.run.(*fakeRunner).script = []fakeCall{
		{match: "inspect", out: "exited\t" + hash + "\n"},
		{match: "start ao-sbx-s1"},
		{match: "command -v \"$1\" sh claude"},
	}
	if _, err := s.Ensure(context.Background(), perAgentReq(ws)); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureDockerUnavailable(t *testing.T) {
	s, _, data := testSandbox(t, []fakeCall{
		{match: "inspect", err: errors.New("Cannot connect to the Docker daemon at unix:///var/run/docker.sock")},
	})
	_, err := s.Ensure(context.Background(), perAgentReq(filepath.Join(data, "worktrees", "p", "s1")))
	if !errors.Is(err, ports.ErrSandboxUnavailable) {
		t.Fatalf("err = %v, want ErrSandboxUnavailable", err)
	}
}

func TestEnsureMissingHarnessBinary(t *testing.T) {
	s, _, data := testSandbox(t, []fakeCall{
		{match: "inspect", err: errors.New("No such object")},
		{match: "run -d"},
		{match: "command -v \"$1\" sh claude", err: errors.New("exit status 1")},
		// The container this call created is removed, not leaked.
		{match: "rm -f ao-sbx-s1"},
	})
	_, err := s.Ensure(context.Background(), perAgentReq(filepath.Join(data, "worktrees", "p", "s1")))
	if !errors.Is(err, ports.ErrSandboxMisconfigured) || !strings.Contains(err.Error(), `node:22`) || !strings.Contains(err.Error(), `"claude"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestEnsureSharedRejectsWorkspaceOutsideManagedRoot(t *testing.T) {
	s, _, _ := testSandbox(t, nil)
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
		{match: "inspect", err: errors.New("No such object")},
		{match: "run -d"},
		{match: "command -v \"$1\""},
	}
	target, err := s.Ensure(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if target.Mode != domain.SandboxModeShared {
		t.Fatalf("target mode = %q, want shared", target.Mode)
	}
	run := runCall(t, fr)
	if strings.Contains(run, "ao.session=") || !strings.Contains(run, bindArg(filepath.Join(data, "worktrees", "p"))) {
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

func TestExecInvalidEnvKeyIsMisconfigured(t *testing.T) {
	s, fr, data := testSandbox(t, nil)
	target := s.targetFor("ao-sbx-s1", "p", "s1")
	if err := os.MkdirAll(target.SessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := s.Exec(target, filepath.Join(data, "w"), map[string]string{"MY-VAR": "x"}, []string{"claude"})
	if !errors.Is(err, ports.ErrSandboxMisconfigured) || !strings.Contains(err.Error(), "MY-VAR") {
		t.Fatalf("err = %v, want ErrSandboxMisconfigured naming the key", err)
	}
	if len(fr.calls) != 0 {
		t.Fatalf("docker calls = %v", fr.calls)
	}
}

func TestDiscardRemovesPerAgentContainer(t *testing.T) {
	s, _, _ := testSandbox(t, []fakeCall{{match: "rm -f ao-sbx-s1"}})
	target := s.targetFor("ao-sbx-s1", "p", "s1")
	target.Mode = domain.SandboxModePerAgent
	if err := s.Discard(context.Background(), target); err != nil {
		t.Fatal(err)
	}
}

func TestDiscardLeavesSharedContainer(t *testing.T) {
	s, fr, _ := testSandbox(t, nil)
	target := s.targetFor("ao-sbx-p-shared", "p", "s1")
	target.Mode = domain.SandboxModeShared
	if err := s.Discard(context.Background(), target); err != nil || len(fr.calls) != 0 {
		t.Fatalf("discard = %v, calls %v; want a no-op", err, fr.calls)
	}
}

func TestDiscardDockerUnavailableIsNil(t *testing.T) {
	s, _, _ := testSandbox(t, []fakeCall{{match: "rm -f ao-sbx-s1", err: errors.New("Cannot connect to the Docker daemon at unix:///var/run/docker.sock")}})
	target := s.targetFor("ao-sbx-s1", "p", "s1")
	target.Mode = domain.SandboxModePerAgent
	if err := s.Discard(context.Background(), target); err != nil {
		t.Fatalf("discard = %v, want nil when Docker is unavailable", err)
	}
}

func notUsed(context.Context) (bool, error) { return false, nil }

func TestReleaseRemovesSharedContainersOnlyWhenUnused(t *testing.T) {
	s, fr, _ := testSandbox(t, nil)
	inUse := func(context.Context) (bool, error) { return true, nil }
	if err := s.Release(context.Background(), "p", inUse); err != nil || len(fr.calls) != 0 {
		t.Fatalf("in-use release = %v, calls %v", err, fr.calls)
	}
	fr.script = []fakeCall{
		{match: "ps -a --filter label=ao.project=p --filter label=ao.sandbox=shared", out: "c1\nc2\n"},
		{match: "rm -f c1"},
		{match: "rm -f c2"},
	}
	if err := s.Release(context.Background(), "p", notUsed); err != nil {
		t.Fatal(err)
	}
}

func TestReleaseKeepsContainersWhenInUseCheckFails(t *testing.T) {
	s, fr, _ := testSandbox(t, nil)
	err := s.Release(context.Background(), "p", func(context.Context) (bool, error) { return false, errors.New("db down") })
	if err != nil || len(fr.calls) != 0 {
		t.Fatalf("release = %v, calls %v", err, fr.calls)
	}
}

func TestEnsureRunFailureIsMisconfigured(t *testing.T) {
	s, _, data := testSandbox(t, []fakeCall{
		{match: "inspect", err: errors.New("No such object")},
		{match: "run -d", err: errors.New("pull access denied for node")},
	})
	_, err := s.Ensure(context.Background(), perAgentReq(filepath.Join(data, "worktrees", "p", "s1")))
	if !errors.Is(err, ports.ErrSandboxMisconfigured) || !strings.Contains(err.Error(), "node:22") {
		t.Fatalf("err = %v", err)
	}
}

func TestEnsureDockerSocketPermissionDenied(t *testing.T) {
	s, _, data := testSandbox(t, []fakeCall{
		{match: "inspect", err: errors.New("permission denied while trying to connect to the Docker daemon socket at unix:///var/run/docker.sock")},
	})
	_, err := s.Ensure(context.Background(), perAgentReq(filepath.Join(data, "worktrees", "p", "s1")))
	if !errors.Is(err, ports.ErrSandboxUnavailable) {
		t.Fatalf("err = %v, want ErrSandboxUnavailable", err)
	}
}

func TestEnsureRunningContainerWithStaleHashIsReused(t *testing.T) {
	s, fr, data := testSandbox(t, []fakeCall{
		{match: "inspect", out: "running\tstale\n"},
		{match: "command -v"},
	})
	if _, err := s.Ensure(context.Background(), perAgentReq(filepath.Join(data, "worktrees", "p", "s1"))); err != nil {
		t.Fatal(err)
	}
	for _, c := range fr.calls {
		if strings.Contains(c, " rm ") || strings.Contains(c, " run ") {
			t.Fatalf("running container was touched: %s", c)
		}
	}
}

func TestEnsureStoppedContainerWithStaleHashIsRecreated(t *testing.T) {
	s, _, data := testSandbox(t, []fakeCall{
		{match: "inspect", out: "exited\tstale\n"},
		{match: "rm -f ao-sbx-s1"},
		{match: "run -d --init --name ao-sbx-s1"},
		{match: "command -v"},
	})
	if _, err := s.Ensure(context.Background(), perAgentReq(filepath.Join(data, "worktrees", "p", "s1"))); err != nil {
		t.Fatal(err)
	}
}
