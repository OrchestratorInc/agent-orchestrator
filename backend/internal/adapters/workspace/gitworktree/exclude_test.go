package gitworktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestAddExcludeTakesEffectInLinkedWorktree(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	root := filepath.Join(tmp, "managed")
	ws, err := New(Options{Binary: git, ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()

	info, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/exclude"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	pattern := "/.ao/attachments/"
	if err := ws.AddExclude(ctx, info, pattern); err != nil {
		t.Fatalf("AddExclude: %v", err)
	}

	attachDir := filepath.Join(info.Path, ".ao", "attachments")
	if err := os.MkdirAll(attachDir, 0o750); err != nil {
		t.Fatalf("mkdir attachments: %v", err)
	}
	if err := os.WriteFile(filepath.Join(attachDir, "pasted.png"), []byte("not really a png"), 0o644); err != nil {
		t.Fatalf("write attachment: %v", err)
	}

	out, err := exec.Command(git, "-C", info.Path, "status", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("exclude did not take effect; git status --porcelain not empty:\n%s", out)
	}

	if err := ws.AddExclude(ctx, info, pattern); err != nil {
		t.Fatalf("AddExclude (repeat): %v", err)
	}
	commonDir, err := exec.Command(git, "-C", info.Path, "rev-parse", "--git-common-dir").Output()
	if err != nil {
		t.Fatalf("git rev-parse --git-common-dir: %v", err)
	}
	excludePath := filepath.Join(strings.TrimSpace(string(commonDir)), "info", "exclude")
	if !filepath.IsAbs(excludePath) {
		excludePath = filepath.Join(info.Path, excludePath)
	}
	data, err := os.ReadFile(excludePath)
	if err != nil {
		t.Fatalf("read exclude: %v", err)
	}
	if got := strings.Count(string(data), pattern); got != 1 {
		t.Errorf("pattern written %d times, want 1: %q", got, data)
	}
}

func TestAddExcludeRejectsUnmanagedPath(t *testing.T) {
	root := t.TempDir()
	repo := t.TempDir()
	ws, err := New(Options{ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	info := ports.WorkspaceInfo{Path: "/etc", SessionID: "proj-1"}
	if err := ws.AddExclude(context.Background(), info, "/x/"); err == nil {
		t.Fatal("expected AddExclude to reject a path outside the managed root")
	}
}

func TestAddExcludeNoPatternsIsNoop(t *testing.T) {
	ws, err := New(Options{ManagedRoot: t.TempDir(), RepoResolver: StaticRepoResolver{"proj": t.TempDir()}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ws.run = func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		t.Fatal("run must not be called when there are no patterns")
		return nil, nil
	}
	if err := ws.AddExclude(context.Background(), ports.WorkspaceInfo{Path: "/whatever"}); err != nil {
		t.Fatalf("AddExclude with no patterns: %v", err)
	}
}
