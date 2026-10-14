//go:build dockersmoke

package dockersandbox

// Run against a real Docker daemon:
//
//	AO_SANDBOX_SMOKE_DIR=<dir under $HOME> go test -tags dockersmoke -run TestDockerSmoke -v ./internal/adapters/sandbox/dockersandbox/
//
// The dir must be under $HOME: macOS temp dirs show as root-owned inside
// Docker Desktop containers whatever the git config says.

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestDockerSmoke(t *testing.T) {
	root := os.Getenv("AO_SANDBOX_SMOKE_DIR")
	if root == "" {
		t.Skip("set AO_SANDBOX_SMOKE_DIR to a dir under $HOME")
	}
	base, err := os.MkdirTemp(root, "smoke-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	repo, ws, data := filepath.Join(base, "repo"), filepath.Join(base, "wt"), filepath.Join(base, "data")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "init", "-q")
	gitRun(t, repo, "config", "user.name", "Smoke Test")
	gitRun(t, repo, "config", "user.email", "smoke@example.com")
	gitRun(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "wt", ws)

	s := New(Config{DataDir: data, ManagedRoot: filepath.Join(data, "worktrees")})
	ctx := context.Background()
	target, err := s.Ensure(ctx, ports.SandboxRequest{SessionID: "smoke1", ProjectID: "smoke", RepoPath: repo, WorkspacePath: ws,
		RequireBinaries: []string{"git"}, Config: domain.SandboxConfig{Mode: domain.SandboxModePerAgent, Image: "alpine/git:2.49.1"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Command(target.Docker, "rm", "-f", target.Container).Run() })
	t.Logf("container %s identity %q <%q>", target.Container, target.GitUserName, target.GitUserEmail)

	argv, err := s.Exec(target, ws, nil, []string{"git", "commit", "--allow-empty", "-m", "from-sandbox"})
	if err != nil {
		t.Fatal(err)
	}
	argv = slices.DeleteFunc(argv, func(a string) bool { return a == "-t" }) // no TTY here
	// Commit exits 0 but still reports that it cannot lock packed-refs,
	// which the read-only git dir refuses.
	out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	t.Logf("in-container git commit: err=%v\n%s", err, out)
	if err != nil {
		t.Fatal("git commit failed inside the sandbox")
	}
	got := gitRun(t, ws, "log", "-1", "--format=%s|%an|%ae")
	t.Logf("host git log -1: %s", got)
	if got != "from-sandbox|Smoke Test|smoke@example.com" {
		t.Fatalf("host sees %q", got)
	}

	gitDir := gitRun(t, repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	admin, err := findWorktreeAdmin(gitDir, ws)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(gitDir, "commondir"), filepath.Join(gitDir, "config"), filepath.Join(gitDir, "hooks", "post-checkout"),
		filepath.Join(admin, "commondir"), filepath.Join(admin, "gitdir"), filepath.Join(ws, ".git")} {
		out, err := exec.Command(target.Docker, "exec", target.Container, "sh", "-c", `echo x > "$1"`, "sh", p).CombinedOutput()
		t.Logf("write %s: err=%v %s", p, err, strings.TrimSpace(string(out)))
		if err == nil {
			t.Fatalf("container could write %s", p)
		}
	}
	// Reflogs are container-local: links planted there never reach the host.
	for _, p := range []string{filepath.Join(gitDir, "logs", "HEAD"), filepath.Join(admin, "logs", "HEAD")} {
		out, err := exec.Command(target.Docker, "exec", target.Container, "ln", "-sf", "/tmp/x", p).CombinedOutput()
		t.Logf("in-container ln -sf /tmp/x %s: err=%v %s", p, err, strings.TrimSpace(string(out)))
		if fi, lerr := os.Lstat(p); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("planted reflog symlink %s reached the host", p)
		}
	}
	t.Logf("host git log -g -1 (repo): %s", gitRun(t, repo, "log", "-g", "-1", "--format=%gd %gs"))
	t.Logf("host git log -g -1 (worktree): %s", gitRun(t, ws, "log", "-g", "-1", "--format=%gd %gs"))
	t.Logf("host git status: %s", gitRun(t, ws, "status", "--short", "--branch"))
}

// TestDockerSmokeHostIsolation proves a sandbox cannot reach a loopback
// listener on the host, keeps internet egress, and stays blocked after a
// container restart once Ensure runs again.
func TestDockerSmokeHostIsolation(t *testing.T) {
	root := os.Getenv("AO_SANDBOX_SMOKE_DIR")
	if root == "" {
		t.Skip("set AO_SANDBOX_SMOKE_DIR to a dir under $HOME")
	}
	base, err := os.MkdirTemp(root, "smoke-net-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	repo, ws, data := filepath.Join(base, "repo"), filepath.Join(base, "wt"), filepath.Join(base, "data")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "init", "-q")
	gitRun(t, repo, "-c", "user.name=s", "-c", "user.email=s@example.com", "commit", "-q", "--allow-empty", "-m", "init")
	gitRun(t, repo, "worktree", "add", "-q", "-b", "wt", ws)

	port := freePort(t)
	srv := exec.Command("python3", "-m", "http.server", strconv.Itoa(port), "--bind", "127.0.0.1")
	srv.Dir = base
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Process.Kill(); _ = srv.Wait() })
	waitListening(t, port)
	url := "http://host.docker.internal:" + strconv.Itoa(port) + "/"

	// Baseline: an unisolated container reaches the host listener.
	out, err := exec.Command("docker", "run", "--rm", "alpine:3.22", "wget", "-q", "-O", "/dev/null", "-T3", url).CombinedOutput()
	t.Logf("baseline (plain container) wget %s: err=%v %s", url, err, strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatal("baseline container cannot reach the host listener; the smoke test proves nothing")
	}
	// The helper's probe tool must see the same listener, or the probe proves nothing.
	out, err = exec.Command("docker", "run", "--rm", isolationImage, "nc", "-z", "-w", "2", "host.docker.internal", strconv.Itoa(port)).CombinedOutput()
	t.Logf("baseline helper nc -z host.docker.internal %d: err=%v %s", port, err, strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatal("the helper's nc probe misses a reachable host listener")
	}

	s := New(Config{DataDir: data, ManagedRoot: filepath.Join(data, "worktrees"), DaemonPort: port})
	ctx := context.Background()
	req := ports.SandboxRequest{SessionID: "smokenet1", ProjectID: "smoke", RepoPath: repo, WorkspacePath: ws,
		Config: domain.SandboxConfig{Mode: domain.SandboxModePerAgent, Image: "alpine:3.22"}}
	target, err := s.Ensure(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Command(target.Docker, "rm", "-f", target.Container).Run() })
	dexec := func(args ...string) error {
		out, err := exec.Command(target.Docker, append([]string{"exec", target.Container}, args...)...).CombinedOutput()
		t.Logf("docker exec %s: err=%v %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		return err
	}
	requireBlocked := func(when string) {
		t.Helper()
		if dexec("wget", "-qO-", "-T3", url) == nil {
			t.Fatalf("%s: sandbox reached the host listener", when)
		}
	}
	requireBlocked("after Ensure")
	_ = dexec("ip", "route")
	if dexec("ip", "route", "del", "blackhole", "192.168.65.254/32") == nil {
		t.Fatal("agent removed the blackhole route")
	}
	if dexec("wget", "-q", "-T5", "-O", "/dev/null", "http://example.com") != nil {
		t.Fatal("internet egress is blocked")
	}

	if out, err := exec.Command(target.Docker, "restart", target.Container).CombinedOutput(); err != nil {
		t.Fatalf("docker restart: %v %s", err, out)
	}
	t.Log("restarted container; routes before Ensure:")
	_ = dexec("ip", "route")
	if _, err := s.Ensure(ctx, req); err != nil {
		t.Fatal(err)
	}
	requireBlocked("after restart + Ensure")
	_ = dexec("ip", "route")
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

func waitListening(t *testing.T, port int) {
	t.Helper()
	for i := 0; i < 50; i++ {
		if c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 200*time.Millisecond); err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("listener on %d never came up", port)
}
