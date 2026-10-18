package dockersandbox

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func writeOutside(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func requireUnchanged(t *testing.T, path, body string) {
	t.Helper()
	if b, err := os.ReadFile(path); err != nil || string(b) != body {
		t.Fatalf("outside file %s = %q, %v; want unchanged %q", path, b, err, body)
	}
}

func execTarget(t *testing.T, s *Sandbox) ports.SandboxTarget {
	t.Helper()
	target := s.targetFor("ao-sbx-s1", "p", "s1")
	if err := os.MkdirAll(target.SessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	return target
}

func requireEnvFile(t *testing.T, target ports.SandboxTarget, want string) {
	t.Helper()
	path := filepath.Join(target.SessionDir, "env")
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o600 {
		t.Fatalf("env = %v, %v; want a regular 0600 file", fi, err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), want) {
		t.Fatalf("env file missing %q:\n%s", want, b)
	}
}

// C1b: the container owns the session dir and can plant links at the env
// file's temp and final names.
func TestExecDoesNotFollowPlantedEnvTmpSymlink(t *testing.T) {
	s, _, data := testSandbox(t, nil)
	target := execTarget(t, s)
	victim := writeOutside(t, "IMPORTANT HOST FILE")
	if err := os.Symlink(victim, filepath.Join(target.SessionDir, "env.tmp")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(target, filepath.Join(data, "w"), map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "tok"}, []string{"claude"}); err != nil {
		t.Fatal(err)
	}
	requireUnchanged(t, victim, "IMPORTANT HOST FILE")
	requireEnvFile(t, target, "CLAUDE_CODE_OAUTH_TOKEN='tok'")
}

func TestExecReplacesPlantedEnvSymlink(t *testing.T) {
	s, _, data := testSandbox(t, nil)
	target := execTarget(t, s)
	victim := writeOutside(t, "IMPORTANT HOST FILE")
	if err := os.Symlink(victim, filepath.Join(target.SessionDir, "env")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(target, filepath.Join(data, "w"), map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "tok"}, []string{"claude"}); err != nil {
		t.Fatal(err)
	}
	requireUnchanged(t, victim, "IMPORTANT HOST FILE")
	requireEnvFile(t, target, "CLAUDE_CODE_OAUTH_TOKEN='tok'")
}

// A symlinked session dir must not redirect the env write outside the
// project dir (shared mode lets the container replace sessions/<sid>).
func TestExecRefusesSymlinkedSessionDir(t *testing.T) {
	s, _, data := testSandbox(t, nil)
	target := s.targetFor("ao-sbx-s1", "p", "s1")
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(target.SessionDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, target.SessionDir); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(target, filepath.Join(data, "w"), map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "tok"}, []string{"claude"}); err == nil {
		t.Fatal("Exec wrote through a symlinked session dir")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("wrote outside the sandbox: %v", entries)
	}
}

// Minor: AO-set paths are shell-quoted like every other value.
func TestExecQuotesAOPaths(t *testing.T) {
	data := filepath.Join(t.TempDir(), "it's $(touch pwned)")
	s := newWithRunner(Config{DataDir: data, ManagedRoot: filepath.Join(data, "worktrees")}, &fakeRunner{t: t}, "/usr/bin/docker")
	target := execTarget(t, s)
	if _, err := s.Exec(target, "/w", nil, []string{"claude"}); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-c", `set -a; . "$1"; printf '%s\n%s' "$HOME" "$AO_SANDBOX_RELAY"`, "sh", filepath.Join(target.SessionDir, "env"))
	cmd.Dir = t.TempDir() // an unquoted path would run `touch pwned` here
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cmd.Dir, "pwned")); err == nil {
		t.Fatal("sourcing the env file ran a command embedded in the data dir path")
	}
	want := filepath.Join(target.ProjectDir, "home") + "\n" + target.RelayDir
	if string(out) != want {
		t.Fatalf("sourced env = %q, want %q", out, want)
	}
}

// C1c: every state dir the container can replace with a link is recreated as
// a real directory, and nothing is created through the link.
func TestEnsureReplacesSymlinkedStateDirs(t *testing.T) {
	for _, rel := range []string{"home", "claude", "sessions", filepath.Join("sessions", "s1"), filepath.Join("sessions", "s1", "relay"), filepath.Join("sessions", "s1", "relay", "req")} {
		t.Run(rel, func(t *testing.T) {
			s, _, data := testSandbox(t, []fakeCall{
				{match: "inspect", err: errors.New("No such object")},
				{match: "run -d"},
				{match: "command -v"},
			})
			projectDir := filepath.Join(data, "sandbox", "projects", "p")
			planted := filepath.Join(projectDir, rel)
			if err := os.MkdirAll(filepath.Dir(planted), 0o700); err != nil {
				t.Fatal(err)
			}
			outside := t.TempDir()
			if err := os.Symlink(outside, planted); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Ensure(context.Background(), perAgentReq(filepath.Join(data, "worktrees", "p", "s1"))); err != nil {
				t.Fatal(err)
			}
			if entries, _ := os.ReadDir(outside); len(entries) != 0 {
				t.Fatalf("created through the planted link: %v", entries)
			}
			for _, d := range []string{"home", "claude", filepath.Join("sessions", "s1", "relay", "req"),
				filepath.Join("sessions", "s1", "relay", "work"), filepath.Join("sessions", "s1", "relay", "resp")} {
				if fi, err := os.Lstat(filepath.Join(projectDir, d)); err != nil || !fi.IsDir() {
					t.Fatalf("%s = %v, %v; want a real directory", d, fi, err)
				}
			}
		})
	}
}

// C3: a shared container sees the project's worker worktrees but not the
// orchestrator's.
func TestEnsureSharedHidesOrchestratorWorktree(t *testing.T) {
	s, fr, data := testSandbox(t, []fakeCall{
		{match: "inspect", err: errors.New("No such object")},
		{match: "run -d"},
		{match: "command -v"},
	})
	req := perAgentReq(filepath.Join(data, "worktrees", "p", "s1"))
	req.Config.Mode = domain.SandboxModeShared
	if _, err := s.Ensure(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	orch := filepath.Join(data, "worktrees", "p", "orchestrator")
	want := "--mount type=tmpfs,destination=" + orch + ",readonly"
	if !strings.Contains(runCall(t, fr), want) {
		t.Fatalf("shared run args missing %q:\n%s", want, runCall(t, fr))
	}
	// AO creates the mountpoint as the host user, so a native Linux engine
	// never creates it root-owned in the managed worktree root.
	if fi, err := os.Lstat(orch); err != nil || !fi.IsDir() {
		t.Fatalf("orchestrator mountpoint not created: %v", err)
	}
}

// The agent can replace the orchestrator mountpoint, which sits in its
// writable work root, with a symlink; Ensure must refuse to mount over it.
func TestEnsureSharedRejectsSymlinkedOrchestratorMountpoint(t *testing.T) {
	s, fr, data := testSandbox(t, nil)
	req := perAgentReq(filepath.Join(data, "worktrees", "p", "s1"))
	req.Config.Mode = domain.SandboxModeShared
	if err := os.Symlink(t.TempDir(), filepath.Join(data, "worktrees", "p", "orchestrator")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ensure(context.Background(), req); !errors.Is(err, ports.ErrSandboxMisconfigured) {
		t.Fatalf("err = %v, want ErrSandboxMisconfigured", err)
	}
	if countCalls(fr, " run -d ") != 0 {
		t.Fatalf("container started over a symlinked mountpoint:\n%s", strings.Join(fr.calls, "\n"))
	}
}

// I6: a per-agent container reads only its own session's prompts.
func TestEnsurePerAgentMountsOnlyOwnPrompts(t *testing.T) {
	s, fr, data := testSandbox(t, []fakeCall{
		{match: "inspect", err: errors.New("No such object")},
		{match: "run -d"},
		{match: "command -v"},
	})
	if _, err := s.Ensure(context.Background(), perAgentReq(filepath.Join(data, "worktrees", "p", "s1"))); err != nil {
		t.Fatal(err)
	}
	all := filepath.Join(data, "prompts")
	if strings.Contains(runCall(t, fr), roBindArg(all)) {
		t.Fatalf("per-agent container mounts every session's prompts:\n%s", runCall(t, fr))
	}
	if fi, err := os.Stat(filepath.Join(all, "s1")); err != nil || !fi.IsDir() {
		t.Fatalf("own prompts dir not created: %v", err)
	}
}

func TestEnsureSharedKeepsPromptsRoot(t *testing.T) {
	s, _, data := testSandbox(t, nil)
	req := perAgentReq(filepath.Join(data, "worktrees", "p", "s1"))
	req.Config.Mode = domain.SandboxModeShared
	m, err := s.mounts(req, gitLayout{dir: gitDirOf(data)})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(findMount(m, filepath.Join(data, "prompts")), &mount{Path: filepath.Join(data, "prompts"), ReadOnly: true}) {
		t.Fatalf("shared mounts = %+v, want the prompts root read-only", m)
	}
}

// The system prompt sends deliverables to the host artifact dir, so it must be
// mounted writable: per-agent gets its own dir, shared the artifacts root.
func TestEnsureMountsArtifactDirWritable(t *testing.T) {
	s, fr, data := testSandbox(t, []fakeCall{
		{match: "inspect", err: errors.New("No such object")},
		{match: "run -d"},
		{match: "command -v"},
	})
	req := perAgentReq(filepath.Join(data, "worktrees", "p", "s1"))
	if _, err := s.Ensure(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	own := filepath.Join(data, "artifacts", "s1")
	if !strings.Contains(runCall(t, fr), bindArg(own)) || strings.Contains(runCall(t, fr), bindArg(filepath.Join(data, "artifacts"))) {
		t.Fatalf("per-agent run should bind only %s read-write:\n%s", own, runCall(t, fr))
	}
	if fi, err := os.Stat(own); err != nil || !fi.IsDir() {
		t.Fatalf("own artifact dir not created: %v", err)
	}
	req.Config.Mode = domain.SandboxModeShared
	m, err := s.mounts(req, gitLayout{dir: gitDirOf(data)})
	if err != nil {
		t.Fatal(err)
	}
	if root := filepath.Join(data, "artifacts"); !reflect.DeepEqual(findMount(m, root), &mount{Path: root}) {
		t.Fatalf("shared mounts = %+v, want the artifacts root read-write", m)
	}
}

// Session state marks a session as sandboxed; a symlink planted in its place
// does not count.
func TestHasSessionState(t *testing.T) {
	s, _, data := testSandbox(t, nil)
	if s.HasSessionState("p", "s1") {
		t.Fatal("state reported before Ensure created it")
	}
	if err := os.MkdirAll(SessionStateDir(data, "p", "s1"), 0o700); err != nil {
		t.Fatal(err)
	}
	if !s.HasSessionState("p", "s1") {
		t.Fatal("state dir not reported")
	}
	if err := os.Symlink(t.TempDir(), SessionStateDir(data, "p", "s2")); err != nil {
		t.Fatal(err)
	}
	if s.HasSessionState("p", "s2") {
		t.Fatal("symlinked state dir counted as state")
	}
}

// The env file is deleted while the state dir stays as the sandbox marker. A
// symlink planted as env is removed itself; its target survives.
func TestRemoveSessionEnv(t *testing.T) {
	s, _, data := testSandbox(t, nil)
	if err := s.RemoveSessionEnv("p", "s1"); err != nil {
		t.Fatalf("missing project dir: %v", err)
	}
	dir := SessionStateDir(data, "p", "s1")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "env"), []byte("export ANTHROPIC_API_KEY=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveSessionEnv("p", "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "env")); !os.IsNotExist(err) {
		t.Fatalf("env still present: %v", err)
	}
	if !s.HasSessionState("p", "s1") {
		t.Fatal("state dir removed with the env file")
	}
	if err := s.RemoveSessionEnv("p", "s1"); err != nil {
		t.Fatalf("second removal: %v", err)
	}

	victim := filepath.Join(t.TempDir(), "keep")
	if err := os.WriteFile(victim, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dir, "env")); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveSessionEnv("p", "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("symlink target removed: %v", err)
	}
}

func findMount(ms []mount, path string) *mount {
	for i := range ms {
		if ms[i].Path == path {
			return &ms[i]
		}
	}
	return nil
}

// C4b: a reused or started container is never removed on a failed probe, and
// neither is a shared one.
func TestEnsureProbeFailureKeepsContainersItDidNotCreate(t *testing.T) {
	t.Run("started", func(t *testing.T) {
		s, _, data := testSandbox(t, nil)
		ws := filepath.Join(data, "worktrees", "p", "s1")
		s.run.(*fakeRunner).script = []fakeCall{
			{match: "inspect", out: "exited\t" + hashFor(t, s, perAgentReq(ws)) + "\n"},
			{match: "start ao-sbx-s1"},
			{match: "command -v", err: errors.New("exit status 1")},
		}
		if _, err := s.Ensure(context.Background(), perAgentReq(ws)); !errors.Is(err, ports.ErrSandboxMisconfigured) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("shared", func(t *testing.T) {
		s, _, data := testSandbox(t, []fakeCall{
			{match: "inspect", err: errors.New("No such object")},
			{match: "run -d"},
			{match: "command -v", err: errors.New("exit status 1")},
		})
		req := perAgentReq(filepath.Join(data, "worktrees", "p", "s1"))
		req.Config.Mode = domain.SandboxModeShared
		if _, err := s.Ensure(context.Background(), req); !errors.Is(err, ports.ErrSandboxMisconfigured) {
			t.Fatalf("err = %v", err)
		}
	})
}

// Minor: a data dir reached through a symlink (/tmp -> /private/tmp) still
// passes the shared-mode containment check.
func TestEnsureSharedAcceptsPhysicalWorkspaceUnderSymlinkedRoot(t *testing.T) {
	s, fr, data := testSandbox(t, []fakeCall{
		{match: "inspect", err: errors.New("No such object")},
		{match: "run -d"},
		{match: "command -v"},
	})
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(data, link); err != nil {
		t.Fatal(err)
	}
	physical, err := filepath.EvalSymlinks(data)
	if err != nil {
		t.Fatal(err)
	}
	s.cfg.ManagedRoot = filepath.Join(link, "worktrees")
	ws := filepath.Join(physical, "worktrees", "p", "s1")
	if err := os.MkdirAll(ws, 0o700); err != nil {
		t.Fatal(err)
	}
	req := perAgentReq(ws)
	req.Config.Mode = domain.SandboxModeShared
	if _, err := s.Ensure(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(physical, "worktrees", "p")
	if !strings.Contains(runCall(t, fr), bindArg(work)) {
		t.Fatalf("shared mount is not the workspace's own ancestor:\n%s", runCall(t, fr))
	}
}

// I2: kill removes a terminated session's per-agent container by label.
func TestDiscardSessionRemovesPerAgentContainerByLabel(t *testing.T) {
	s, _, _ := testSandbox(t, []fakeCall{
		{match: "ps -a --filter label=ao.sandbox=per-agent --filter label=ao.session=s1 --filter label=ao.project=p", out: "c1\n"},
		{match: "rm -f c1"},
	})
	if err := s.DiscardSession(context.Background(), "p", "s1"); err != nil {
		t.Fatal(err)
	}
}

func TestDiscardSessionDockerUnavailableIsNil(t *testing.T) {
	s, _, _ := testSandbox(t, []fakeCall{
		{match: "ps -a", err: errors.New("Cannot connect to the Docker daemon at unix:///var/run/docker.sock")},
	})
	if err := s.DiscardSession(context.Background(), "p", "s1"); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
}
