package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestNameTaskWorkspace(t *testing.T) {
	for _, multi := range []bool{false, true} {
		t.Run(fmt.Sprintf("multi=%v", multi), func(t *testing.T) {
			git := requireGit(t)
			tmp := t.TempDir()
			repo := setupOriginClone(t, git, filepath.Join(tmp, "root"))
			ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			var info ports.WorkspaceProjectInfo
			if multi {
				child := setupOriginClone(t, git, filepath.Join(tmp, "child"))
				info, err = ws.CreateWorkspaceProject(ctx, ports.WorkspaceProjectConfig{
					ProjectID: "proj", SessionID: "proj-1", Kind: domain.KindWorker, Branch: "ao/proj-1",
					RootRepoPath: repo, BaseBranch: "main", Repos: []ports.WorkspaceProjectRepoConfig{{Name: "child", RepoPath: child, RelativePath: "packages/child", BaseBranch: "main"}},
				})
				if err == nil {
					sha := gitOutput(t, git, child, "rev-parse", "HEAD")
					runGit(t, git, child, "update-ref", "refs/remotes/origin/ao/fix-login-2/root", sha)
				}
			} else {
				info.Root, err = ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "proj-1", Branch: "ao/proj-1/root", BaseBranch: "main"})
				runGit(t, git, repo, "branch", "ao/fix-login-2/tests")
			}
			if err != nil {
				t.Fatal(err)
			}
			occupied := filepath.Join(ws.managedRoot, "proj", "fix-login")
			if err := os.Mkdir(occupied, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(occupied, "sentinel"), []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
			stale, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "fix-login-3", Branch: "abandoned", BaseBranch: "main"})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(stale.Path); err != nil {
				t.Fatal(err)
			}
			oldPath := info.Root.Path
			info, err = ws.NameTaskWorkspace(ctx, info, "ao/fix-login/root", "fix-login")
			if err != nil {
				t.Fatal(err)
			}
			if filepath.Base(info.Root.Path) != "fix-login-4" || info.Root.Branch != "ao/fix-login-4/root" || info.Root.SessionID != "proj-1" {
				t.Fatalf("named workspace = %+v", info)
			}
			if got, err := os.ReadFile(filepath.Join(occupied, "sentinel")); err != nil || string(got) != "keep" {
				t.Fatalf("occupied folder changed: %q, %v", got, err)
			}
			if _, err := os.Stat(oldPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("old path remains: %v", err)
			}
			if got := gitOutput(t, git, info.Root.Path, "branch", "--show-current"); got != info.Root.Branch {
				t.Fatalf("root branch = %q", got)
			}
			for _, child := range info.Worktrees {
				if got := gitOutput(t, git, child.Path, "branch", "--show-current"); got != info.Root.Branch {
					t.Fatalf("child branch = %q", got)
				}
				if got := gitOutput(t, git, child.RepoPath, "worktree", "list", "--porcelain"); !strings.Contains(got, filepath.ToSlash(child.Path)) {
					t.Fatalf("stale registration: %s", got)
				}
			}
			if multi {
				err = ws.DestroyWorkspaceProject(ctx, info)
			} else {
				err = ws.Destroy(ctx, info.Root)
				if err == nil {
					var restored ports.WorkspaceInfo
					restored, err = ws.Restore(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "proj-1", Branch: info.Root.Branch, Path: info.Root.Path, BaseBranch: "main"})
					if err == nil && restored.Path != info.Root.Path {
						t.Fatalf("restore changed named path: %+v", restored)
					}
				}
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNameTaskWorkspaceRollsBackBeforeMove(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(fmt.Sprintf("after=%v", after), func(t *testing.T) {
			git := requireGit(t)
			tmp := t.TempDir()
			repo := setupOriginClone(t, git, tmp)
			ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			root, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "proj-1", Branch: "ao/proj-1/root"})
			if err != nil {
				t.Fatal(err)
			}
			actualRun := ws.run
			ws.run = func(ctx context.Context, binary string, args ...string) ([]byte, error) {
				if strings.Contains(strings.Join(args, " "), "branch -m ao/fix-login/root") {
					if after {
						if _, err := actualRun(ctx, binary, args...); err != nil {
							return nil, err
						}
						return nil, context.Canceled
					}
					return nil, errors.New("injected Git failure")
				}
				return actualRun(ctx, binary, args...)
			}
			info, err := ws.NameTaskWorkspace(ctx, ports.WorkspaceProjectInfo{Root: root}, "ao/fix-login/root", "fix-login")
			if err == nil || info.Root.Path != root.Path || info.Root.Branch != root.Branch {
				t.Fatalf("failed rename = %+v, %v", info, err)
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(root.Path), "fix-login")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("reservation remains: %v", err)
			}
			if got := gitOutput(t, git, root.Path, "branch", "--show-current"); got != root.Branch {
				t.Fatalf("failed naming changed original branch: %s", got)
			}

		})
	}
}

func BenchmarkNameTaskWorkspace(b *testing.B) {
	for _, collisions := range []int{0, 2} {
		b.Run(fmt.Sprintf("collisions=%d", collisions), func(b *testing.B) {
			ctx := context.Background()
			tmp := b.TempDir()
			repo := filepath.Join(tmp, "repo")
			git := func(args ...string) {
				b.Helper()
				if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
					b.Fatalf("git %v: %v: %s", args, err, out)
				}
			}
			git("init", "-b", "main", repo)
			git("-C", repo, "-c", "user.name=AO", "-c", "user.email=ao@example.com", "commit", "--allow-empty", "-m", "seed")
			ws, err := New(Options{ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
			if err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				name := fmt.Sprintf("fix-login-%d", i)
				root, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: domain.SessionID(fmt.Sprintf("proj-%d", i)), Branch: fmt.Sprintf("ao/proj-%d/root", i), BaseBranch: "main"})
				if err != nil {
					b.Fatal(err)
				}
				for n := 0; n < collisions; n++ {
					branch := "ao/" + name
					if n > 0 {
						branch += fmt.Sprintf("-%d", n+1)
					}
					git("-C", repo, "branch", branch+"/root")
				}
				b.StartTimer()
				_, err = ws.NameTaskWorkspace(ctx, ports.WorkspaceProjectInfo{Root: root}, "ao/"+name+"/root", name)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestNameTaskWorkspaceRetriesConcurrentBranchConflict(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	root, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "proj-1", Branch: "ao/proj-1/root"})
	if err != nil {
		t.Fatal(err)
	}
	actualRun := ws.run
	injected := false
	ws.run = func(ctx context.Context, binary string, args ...string) ([]byte, error) {
		if !injected && strings.Contains(strings.Join(args, " "), "branch -m ao/fix-login/root") {
			injected = true
			if _, err := actualRun(ctx, binary, "-C", repo, "branch", "ao/fix-login/root"); err != nil {
				return nil, err
			}
		}
		return actualRun(ctx, binary, args...)
	}
	info, err := ws.NameTaskWorkspace(ctx, ports.WorkspaceProjectInfo{Root: root}, "ao/fix-login/root", "fix-login")
	if err != nil || info.Root.Branch != "ao/fix-login-2/root" || filepath.Base(info.Root.Path) != "fix-login-2" {
		t.Fatalf("concurrent conflict = %+v,%v", info, err)
	}
	if got := gitOutput(t, git, repo, "rev-parse", "ao/fix-login/root"); got != gitOutput(t, git, repo, "rev-parse", "main") {
		t.Fatal("competing branch changed")
	}
}

func TestNameTaskWorkspaceRepairCanResume(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, filepath.Join(tmp, "root"))
	child := setupOriginClone(t, git, filepath.Join(tmp, "child"))
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	info, err := ws.CreateWorkspaceProject(ctx, ports.WorkspaceProjectConfig{ProjectID: "proj", SessionID: "proj-1", Kind: domain.KindWorker, Branch: "ao/proj-1", RootRepoPath: repo, BaseBranch: "main", Repos: []ports.WorkspaceProjectRepoConfig{{Name: "child", RepoPath: child, RelativePath: "packages/child", BaseBranch: "main"}}})
	if err != nil {
		t.Fatal(err)
	}
	actualRun := ws.run
	injected := false
	ws.run = func(ctx context.Context, binary string, args ...string) ([]byte, error) {
		if !injected && strings.Contains(strings.Join(args, " "), "rev-parse --git-common-dir") {
			injected = true
			return nil, errors.New("resolve common Git directory interrupted")
		}
		return actualRun(ctx, binary, args...)
	}
	info, err = ws.NameTaskWorkspace(ctx, info, "ao/fix-login/root", "fix-login")
	if err == nil || filepath.Base(info.Root.Path) != "fix-login" {
		t.Fatalf("partial move=%+v,%v", info, err)
	}
	for _, wt := range info.Worktrees {
		if !strings.HasPrefix(wt.Path, info.Root.Path) {
			t.Fatalf("stale child path: %+v", wt)
		}
	}
	info, err = ws.RepairTaskWorkspace(ctx, info)
	if err != nil {
		t.Fatal(err)
	}
	for _, wt := range info.Worktrees {
		if !strings.Contains(gitOutput(t, git, wt.RepoPath, "worktree", "list", "--porcelain"), filepath.ToSlash(wt.Path)) {
			t.Fatalf("registration not repaired: %+v", wt)
		}
		if got := gitOutput(t, git, wt.Path, "branch", "--show-current"); got != "ao/fix-login/root" {
			t.Fatalf("branch=%q", got)
		}
	}
}

func TestNameTaskWorkspaceDoesNotRepairForeignWorktrees(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, filepath.Join(tmp, "root"))
	foreign := filepath.Join(tmp, "Codex", "foreign")
	gitOutput(t, git, repo, "worktree", "add", "-b", "codex/foreign", foreign, "main")
	foreignGitFile := filepath.Join(foreign, ".git")
	brokenGitFile := []byte("gitdir: " + filepath.Join(tmp, "unavailable", "admin") + "\n")
	if err := os.WriteFile(foreignGitFile, brokenGitFile, 0o444); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(foreignGitFile, 0o644) }()
	child := setupOriginClone(t, git, filepath.Join(tmp, "child"))
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatal(err)
	}
	info, err := ws.CreateWorkspaceProject(context.Background(), ports.WorkspaceProjectConfig{
		ProjectID: "proj", SessionID: "proj-1", Kind: domain.KindWorker, Branch: "ao/proj-1",
		RootRepoPath: repo, BaseBranch: "main",
		Repos: []ports.WorkspaceProjectRepoConfig{{Name: "child", RepoPath: child, RelativePath: "packages/child", BaseBranch: "main"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	actualRun := ws.run
	ws.run = func(ctx context.Context, binary string, args ...string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "worktree repair") {
			t.Fatal("task naming invoked Git's repo-wide worktree repair")
		}
		return actualRun(ctx, binary, args...)
	}
	info, err = ws.NameTaskWorkspace(context.Background(), info, "ao/fix-login/root", "fix-login")
	if err != nil {
		t.Fatal(err)
	}
	for _, wt := range info.Worktrees {
		gitDir, err := checkoutGitDir(wt.Path)
		if err != nil {
			t.Fatal(err)
		}
		backlink, err := os.ReadFile(filepath.Join(gitDir, "gitdir"))
		if err != nil {
			t.Fatal(err)
		}
		if got, want := strings.TrimSpace(string(backlink)), filepath.Join(wt.Path, ".git"); got != want {
			t.Fatalf("%s backlink = %q, want %q", wt.Path, got, want)
		}
	}
	if got, err := os.ReadFile(foreignGitFile); err != nil || string(got) != string(brokenGitFile) {
		t.Fatalf("foreign Codex .git file changed: %q, %v", got, err)
	}
}
