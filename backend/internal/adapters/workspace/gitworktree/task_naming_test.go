package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestRenameTaskBranch(t *testing.T) {
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
			runGit(t, git, repo, "branch", "ao/fix-login/other")
			if multi {
				child := setupOriginClone(t, git, filepath.Join(tmp, "child"))
				info, err = ws.CreateWorkspaceProject(ctx, ports.WorkspaceProjectConfig{
					ProjectID: "proj", SessionID: "proj-1", Kind: domain.KindWorker, Branch: "ao/proj-1",
					RootRepoPath: repo, BaseBranch: "main", Repos: []ports.WorkspaceProjectRepoConfig{{Name: "child", RepoPath: child, RelativePath: "packages/child", BaseBranch: "main"}},
				})
				if err == nil {
					runGit(t, git, child, "update-ref", "refs/remotes/origin/ao/fix-login-2/root", gitOutput(t, git, child, "rev-parse", "HEAD"))
				}
			} else {
				info.Root, err = ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "proj-1", Branch: "ao/proj-1/root", BaseBranch: "main"})
				runGit(t, git, repo, "branch", "ao/fix-login-2/tests")
			}
			if err != nil {
				t.Fatal(err)
			}
			before := info
			info, err = ws.RenameTaskBranch(ctx, info, "ao/fix-login/root")
			if err != nil {
				t.Fatal(err)
			}
			if info.Root.Branch != "ao/fix-login-3/root" || info.Root.Path != before.Root.Path || info.Root.SessionID != "proj-1" {
				t.Fatalf("renamed workspace = %+v", info)
			}
			if got := gitOutput(t, git, info.Root.Path, "branch", "--show-current"); got != info.Root.Branch {
				t.Fatalf("root branch = %q", got)
			}
			if out, err := exec.Command(git, "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/"+before.Root.Branch).CombinedOutput(); err == nil {
				t.Fatalf("temporary branch remains: %s", out)
			}
			for i, child := range info.Worktrees {
				if child.Path != before.Worktrees[i].Path || child.Branch != info.Root.Branch {
					t.Fatalf("child = %+v", child)
				}
				if got := gitOutput(t, git, child.Path, "branch", "--show-current"); got != info.Root.Branch {
					t.Fatalf("child branch = %q", got)
				}
			}
			if multi {
				err = ws.DestroyWorkspaceProject(ctx, info)
			} else {
				err = ws.Destroy(ctx, info.Root)
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRenameTaskBranchRollsBackEveryRepository(t *testing.T) {
	git, ws, info := newTwoRepoTask(t)
	ctx := context.Background()
	actualRun := ws.run
	ws.run = func(ctx context.Context, binary string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, info.Worktrees[len(info.Worktrees)-1].Path) && strings.Contains(joined, "branch -m -- ao/proj-1 ao/fix-login/root") {
			return nil, errors.New("injected Git failure")
		}
		return actualRun(ctx, binary, args...)
	}
	got, err := ws.RenameTaskBranch(ctx, info, "ao/fix-login/root")
	if err == nil || got.Root.Branch != "ao/proj-1" {
		t.Fatalf("failed rename = %+v, %v", got, err)
	}
	for _, wt := range info.Worktrees {
		if branch := gitOutput(t, git, wt.Path, "branch", "--show-current"); branch != "ao/proj-1" {
			t.Fatalf("%s kept partial rename %q", wt.Path, branch)
		}
	}
}

func TestRenameTaskBranchRetriesConcurrentBranchConflict(t *testing.T) {
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
		if !injected && strings.Contains(strings.Join(args, " "), "branch -m -- ao/proj-1/root ao/fix-login/root") {
			injected = true
			if _, err := actualRun(ctx, binary, "-C", repo, "branch", "ao/fix-login/root"); err != nil {
				return nil, err
			}
		}
		return actualRun(ctx, binary, args...)
	}
	info, err := ws.RenameTaskBranch(ctx, ports.WorkspaceProjectInfo{Root: root}, "ao/fix-login/root")
	if err != nil || info.Root.Branch != "ao/fix-login-2/root" || info.Root.Path != root.Path {
		t.Fatalf("concurrent conflict = %+v,%v", info, err)
	}
	if got := gitOutput(t, git, repo, "rev-parse", "ao/fix-login/root"); got != gitOutput(t, git, repo, "rev-parse", "main") {
		t.Fatal("competing branch changed")
	}
}

// A rename after launch must never strand refs the agent already made under the
// temporary namespace, because AO attributes PRs to the session by that prefix.
// Only a pushed temporary branch blocks the rename: a plain `git push` would
// then name a different remote branch. Local work under the temporary name
// stays attributed to the session through that namespace.
func TestRenameTaskBranchGuard(t *testing.T) {
	for name, tc := range map[string]struct {
		act     func(t *testing.T, git, repo, path string)
		renamed bool
	}{
		"sibling":  {func(t *testing.T, git, repo, path string) { runGit(t, git, repo, "branch", "ao/proj-1/feature") }, true},
		"switched": {func(t *testing.T, git, repo, path string) { runGit(t, git, path, "switch", "-c", "elsewhere") }, true},
		"pushed": {func(t *testing.T, git, repo, path string) {
			runGit(t, git, repo, "update-ref", "refs/remotes/origin/ao/proj-1/root", gitOutput(t, git, repo, "rev-parse", "main"))
		}, false},
	} {
		t.Run(name, func(t *testing.T) {
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
			tc.act(t, git, repo, root.Path)
			info, err := ws.RenameTaskBranch(ctx, ports.WorkspaceProjectInfo{Root: root}, "ao/fix-login/root")
			_, showErr := exec.Command(git, "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/ao/fix-login/root").CombinedOutput()
			if tc.renamed != (err == nil && info.Root.Branch == "ao/fix-login/root" && showErr == nil) {
				t.Fatalf("rename = %+v, %v, branch exists=%v", info, err, showErr == nil)
			}
		})
	}
}

func TestRenameTaskBranchToItsOwnName(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatal(err)
	}
	// A workspace project's temporary branch is the bare namespace.
	for i, temporary := range []string{"ao/proj-1/root", "ao/proj-2"} {
		id := fmt.Sprintf("proj-%d", i+1)
		root, err := ws.Create(context.Background(), ports.WorkspaceConfig{ProjectID: "proj", SessionID: domain.SessionID(id), Branch: temporary})
		if err != nil {
			t.Fatal(err)
		}
		info, err := ws.RenameTaskBranch(context.Background(), ports.WorkspaceProjectInfo{Root: root}, "ao/"+id+"/root")
		if err != nil || info.Root.Branch != temporary {
			t.Fatalf("rename %s to own name = %+v, %v", temporary, info, err)
		}
	}
}

// When undoing a partial multi-repository rename also fails, the result still
// says which branches Git holds.
func TestRenameTaskBranchReportsFailedUndo(t *testing.T) {
	git, ws, info := newTwoRepoTask(t)
	root, last := info.Worktrees[0].Path, info.Worktrees[len(info.Worktrees)-1].Path
	actualRun := ws.run
	ws.run = func(ctx context.Context, binary string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if len(args) > 1 && args[1] == last && strings.Contains(joined, "branch -m -- ao/proj-1 ao/fix-login/root") {
			return nil, errors.New("injected rename failure")
		}
		if len(args) > 1 && args[1] == root && strings.Contains(joined, "branch -m -- ao/fix-login/root ao/proj-1") {
			return nil, errors.New("injected undo failure")
		}
		return actualRun(ctx, binary, args...)
	}
	got, err := ws.RenameTaskBranch(context.Background(), info, "ao/fix-login/root")
	if err == nil {
		t.Fatal("undo failure not reported")
	}
	requireBranchesMatchGit(t, git, got)
}

// When the undo stops partway, the result says which branches Git holds.
func TestRevertTaskBranchReportsFailedUndo(t *testing.T) {
	git, ws, original := newTwoRepoTask(t)
	ctx := context.Background()
	renamed, err := ws.RenameTaskBranch(ctx, original, "ao/fix-login/root")
	if err != nil {
		t.Fatal(err)
	}
	last := original.Worktrees[len(original.Worktrees)-1].Path
	actualRun := ws.run
	ws.run = func(ctx context.Context, binary string, args ...string) ([]byte, error) {
		if len(args) > 1 && args[1] == last && strings.Contains(strings.Join(args, " "), "branch -m") {
			return nil, errors.New("injected undo failure")
		}
		return actualRun(ctx, binary, args...)
	}
	got, err := ws.RevertTaskBranch(ctx, renamed, original)
	if err == nil {
		t.Fatal("undo failure not reported")
	}
	requireBranchesMatchGit(t, git, got)
}

// newTwoRepoTask creates a workspace-project task with a root and one child
// repository on the temporary branch ao/proj-1.
func newTwoRepoTask(t *testing.T) (string, *Workspace, ports.WorkspaceProjectInfo) {
	t.Helper()
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, filepath.Join(tmp, "root"))
	child := setupOriginClone(t, git, filepath.Join(tmp, "child"))
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatal(err)
	}
	info, err := ws.CreateWorkspaceProject(context.Background(), ports.WorkspaceProjectConfig{ProjectID: "proj", SessionID: "proj-1", Kind: domain.KindWorker, Branch: "ao/proj-1", RootRepoPath: repo, BaseBranch: "main", Repos: []ports.WorkspaceProjectRepoConfig{{Name: "child", RepoPath: child, RelativePath: "packages/child", BaseBranch: "main"}}})
	if err != nil || len(info.Worktrees) < 2 {
		t.Fatalf("workspace project = %+v, %v", info, err)
	}
	return git, ws, info
}

func requireBranchesMatchGit(t *testing.T, git string, got ports.WorkspaceProjectInfo) {
	t.Helper()
	if actual := gitOutput(t, git, got.Root.Path, "branch", "--show-current"); got.Root.Branch != actual {
		t.Fatalf("root = %q, Git holds %q", got.Root.Branch, actual)
	}
	for _, wt := range got.Worktrees {
		if actual := gitOutput(t, git, wt.Path, "branch", "--show-current"); wt.Branch != actual {
			t.Fatalf("%s = %q, Git holds %q", wt.RepoName, wt.Branch, actual)
		}
	}
}

// RevertTaskBranch restores each repository's own original branch exactly,
// even with refs under the temporary namespace that a rename would skip.
func TestRevertTaskBranchIsExact(t *testing.T) {
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
	original := ports.WorkspaceProjectInfo{Root: root}
	renamed, err := ws.RenameTaskBranch(ctx, original, "ao/fix-login/root")
	if err != nil {
		t.Fatal(err)
	}
	runGit(t, git, repo, "branch", "ao/proj-1/fix")
	if _, err := ws.RevertTaskBranch(ctx, renamed, original); err != nil {
		t.Fatal(err)
	}
	if got := gitOutput(t, git, root.Path, "branch", "--show-current"); got != root.Branch {
		t.Fatalf("reverted branch = %q", got)
	}
}

func BenchmarkRenameTaskBranch(b *testing.B) {
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
		root, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: domain.SessionID(fmt.Sprintf("proj-%d", i)), Branch: fmt.Sprintf("ao/proj-%d/root", i), BaseBranch: "main"})
		if err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		if _, err := ws.RenameTaskBranch(ctx, ports.WorkspaceProjectInfo{Root: root}, fmt.Sprintf("ao/fix-login-%d/root", i)); err != nil {
			b.Fatal(err)
		}
	}
}
