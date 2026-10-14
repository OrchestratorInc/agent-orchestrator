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
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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
	t.Logf("host git status: %s", gitRun(t, ws, "status", "--short", "--branch"))
}
