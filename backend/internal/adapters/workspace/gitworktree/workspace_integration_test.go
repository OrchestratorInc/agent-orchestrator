package gitworktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/filesnapshot"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestWorkspaceIntegrationCreateRestoreDestroy(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	root := filepath.Join(tmp, "managed")
	ws, err := New(Options{Binary: git, ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	cfg := ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/one"}

	info, err := ws.Create(ctx, cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if info.Path != filepath.Join(ws.managedRoot, "proj", "sess") || info.Branch != cfg.Branch || info.SessionID != cfg.SessionID || info.ProjectID != cfg.ProjectID {
		t.Fatalf("info = %#v", info)
	}
	if _, err := os.Stat(filepath.Join(info.Path, "README.md")); err != nil {
		t.Fatalf("created worktree missing seed file: %v", err)
	}

	restored, err := ws.Restore(ctx, cfg)
	if err != nil {
		t.Fatalf("restore registered: %v", err)
	}
	if restored.Path != info.Path || restored.Branch != cfg.Branch {
		t.Fatalf("restored = %#v", restored)
	}

	if err := ws.Destroy(ctx, info); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if _, err := os.Stat(info.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("path after destroy stat err = %v, want not exist", err)
	}

	restored, err = ws.Restore(ctx, cfg)
	if err != nil {
		t.Fatalf("restore after destroy: %v", err)
	}
	if restored.Path != info.Path || restored.Branch != cfg.Branch {
		t.Fatalf("restored after destroy = %#v", restored)
	}
	if err := ws.Destroy(ctx, restored); err != nil {
		t.Fatalf("destroy restored: %v", err)
	}
}

func TestWorkspaceIntegrationRestoreUsesLastCheckedOutBranch(t *testing.T) {
	git := requireGit(t)
	const sessionBranch, prBranch = "ao/sess/root", "ao/sess/fix"
	cases := []struct {
		name  string
		after func(t *testing.T, repo string)
		want  string
	}{
		{"branch still exists", func(*testing.T, string) {}, prBranch},
		{"branch deleted", func(t *testing.T, repo string) { runGit(t, git, repo, "branch", "-D", prBranch) }, sessionBranch},
		{"branch checked out elsewhere", func(t *testing.T, repo string) { runGit(t, git, repo, "switch", prBranch) }, sessionBranch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			repo := setupOriginClone(t, git, tmp)
			ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
			if err != nil {
				t.Fatalf("new: %v", err)
			}
			ctx := context.Background()
			cfg := ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: sessionBranch}
			info, err := ws.Create(ctx, cfg)
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			runGit(t, git, info.Path, "switch", "-c", prBranch)
			runGit(t, git, info.Path, "commit", "--allow-empty", "-m", "agent work")
			if err := ws.Destroy(ctx, info); err != nil {
				t.Fatalf("destroy: %v", err)
			}
			tc.after(t, repo)

			cfg.Path, cfg.LastBranch = info.Path, prBranch
			restored, err := ws.Restore(ctx, cfg)
			if err != nil {
				t.Fatalf("restore: %v", err)
			}
			if restored.Branch != tc.want {
				t.Fatalf("restored.Branch = %q, want %q", restored.Branch, tc.want)
			}
			if got := gitOutput(t, git, restored.Path, "branch", "--show-current"); got != tc.want {
				t.Fatalf("worktree has %q checked out, want %q", got, tc.want)
			}
		})
	}
}

func TestWorkspaceIntegrationExplicitNewBranchRejectsExistingName(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, filepath.Join(tmp, "repo"))
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatal(err)
	}
	for _, fresh := range []bool{false, true} {
		_, err := ws.Create(context.Background(), ports.WorkspaceConfig{ProjectID: "proj", SessionID: "task", Branch: "main", FreshBranch: fresh, RequireNewBranch: true})
		if !errors.Is(err, ports.ErrWorkspaceBranchAlreadyExists) {
			t.Fatalf("fresh=%v, err=%v, want existing branch conflict", fresh, err)
		}
	}
	runGit(t, git, repo, "update-ref", "refs/remotes/origin/remote-only", "refs/heads/main")
	_, err = ws.Create(context.Background(), ports.WorkspaceConfig{ProjectID: "proj", SessionID: "task", Branch: "remote-only", RequireNewBranch: true})
	if !errors.Is(err, ports.ErrWorkspaceBranchAlreadyExists) {
		t.Fatalf("remote branch conflict = %v, want existing branch conflict", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, "managed", "proj", "task")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected task worktree stat = %v, want absent", err)
	}
}

func TestWorkspaceIntegrationExplicitNewBranchRejectsChildCollision(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	rootRepo := setupOriginClone(t, git, filepath.Join(tmp, "root"))
	childRepo := setupOriginClone(t, git, filepath.Join(tmp, "child"))
	runGit(t, git, childRepo, "branch", "fix/login")
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": rootRepo}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ws.CreateWorkspaceProject(context.Background(), ports.WorkspaceProjectConfig{
		ProjectID: "proj", SessionID: "task", Kind: domain.KindWorker, Branch: "fix/login",
		FreshBranch: true, RequireNewBranch: true, RootRepoPath: rootRepo, BaseBranch: "main",
		Repos: []ports.WorkspaceProjectRepoConfig{{Name: "api", RelativePath: "api", RepoPath: childRepo}},
	})
	if !errors.Is(err, ports.ErrWorkspaceBranchAlreadyExists) {
		t.Fatalf("child branch conflict = %v, want existing branch conflict", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, "managed", "proj", "task")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected workspace stat = %v, want absent", err)
	}
}

func TestWorkspaceIntegrationCancelledPreparedAddLeavesNoWorktreeOrBranch(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, filepath.Join(tmp, "repo"))
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatal(err)
	}
	branch := "ao/prepared-cancelled"
	path := filepath.Join(ws.managedRoot, "proj", "prepared-cancelled")
	actualRun := ws.run
	ws.run = func(ctx context.Context, binary string, args ...string) ([]byte, error) {
		out, err := actualRun(ctx, binary, args...)
		if err == nil && strings.Contains(strings.Join(args, " "), "worktree add -b "+branch+" ") {
			return nil, context.Canceled
		}
		return out, err
	}
	_, err = ws.Create(context.Background(), ports.WorkspaceConfig{
		ProjectID: "proj", SessionID: "prepared-cancelled", Kind: domain.KindWorker,
		Branch: branch, FreshBranch: true, BaseBranch: "main",
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Create error = %v, want cancellation", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled worktree remains at %q: %v", path, err)
	}
	if output, err := exec.Command(git, "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch).CombinedOutput(); err == nil {
		t.Fatalf("cancelled branch remains: %s", output)
	}
}

func TestWorkspaceIntegrationCancelledPreparedAddPreservesUnregisteredPartialDirectory(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, filepath.Join(tmp, "repo"))
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatal(err)
	}
	branch := "ao/partial"
	path := filepath.Join(ws.managedRoot, "proj", "partial")
	actualRun := ws.run
	ws.run = func(ctx context.Context, binary string, args ...string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "worktree add -b "+branch+" ") {
			if err := os.MkdirAll(path, 0o755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(filepath.Join(path, "partial.txt"), []byte("keep this\n"), 0o644); err != nil {
				return nil, err
			}
			seed := gitOutput(t, git, repo, "rev-parse", "main")
			runGit(t, git, repo, "update-ref", "refs/heads/"+branch, seed)
			return nil, context.Canceled
		}
		return actualRun(ctx, binary, args...)
	}
	info, err := ws.Create(context.Background(), ports.WorkspaceConfig{
		ProjectID: "proj", SessionID: "partial", Kind: domain.KindWorker,
		Branch: branch, FreshBranch: true, BaseBranch: "main",
	})
	if !errors.Is(err, ports.ErrWorkspaceDirty) || info.Path != path || info.BaseSHA == "" {
		t.Fatalf("partial create = (%+v, %v), want preserved path and creation SHA", info, err)
	}
	if err := ws.Destroy(context.Background(), info); !errors.Is(err, ports.ErrWorkspaceDirty) {
		t.Fatalf("Destroy unregistered partial worktree = %v, want preservation", err)
	}
	if _, err := os.Stat(filepath.Join(path, "partial.txt")); err != nil {
		t.Fatalf("partial user file disappeared: %v", err)
	}
	if err := os.Remove(filepath.Join(path, "partial.txt")); err != nil {
		t.Fatal(err)
	}
	if err := ws.Destroy(context.Background(), info); err != nil {
		t.Fatal(err)
	}
	if err := ws.DeletePreparedBranch(context.Background(), info); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cleaned partial directory still exists: %v", err)
	}
}

func TestWorkspaceIntegrationWorkspaceProjectUsesResolvedNonOriginSeed(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	rootRepo := setupOriginClone(t, git, filepath.Join(tmp, "root"))
	childRepo := setupOriginClone(t, git, filepath.Join(tmp, "child"))
	branch := "ao/seeded"
	runGit(t, git, childRepo, "remote", "rename", "origin", "upstream")
	runGit(t, git, childRepo, "checkout", "-b", "seed-source")
	if err := os.WriteFile(filepath.Join(childRepo, "seed.txt"), []byte("from upstream\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, git, childRepo, "add", "seed.txt")
	runGit(t, git, childRepo, "commit", "-m", "seed requested branch")
	wantSeed := gitOutput(t, git, childRepo, "rev-parse", "HEAD")
	runGit(t, git, childRepo, "update-ref", "refs/remotes/upstream/"+branch, wantSeed)
	runGit(t, git, childRepo, "checkout", "main")
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": rootRepo}})
	if err != nil {
		t.Fatal(err)
	}
	info, err := ws.CreateWorkspaceProject(context.Background(), ports.WorkspaceProjectConfig{
		ProjectID: "proj", SessionID: "seeded", Kind: domain.KindWorker, Branch: branch,
		RootRepoPath: rootRepo, BaseBranch: "main",
		Repos: []ports.WorkspaceProjectRepoConfig{{Name: "api", RelativePath: "api", RepoPath: childRepo}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ws.DestroyWorkspaceProject(context.Background(), info) }()
	if got := gitOutput(t, git, info.Worktrees[1].Path, "rev-parse", "HEAD"); got != wantSeed {
		t.Fatalf("child HEAD = %q, want pre-resolved upstream seed %q", got, wantSeed)
	}
}

func TestWorkspaceIntegrationRecoveredPreparedBranchWithoutDurableSHAIsPreserved(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	rootRepo := setupOriginClone(t, git, filepath.Join(tmp, "root"))
	childRepo := setupOriginClone(t, git, filepath.Join(tmp, "child"))
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": rootRepo}})
	if err != nil {
		t.Fatal(err)
	}
	cfg := ports.WorkspaceProjectConfig{
		ProjectID: "proj", SessionID: "prepared", Kind: domain.KindWorker,
		Branch: "ao/prepared", FreshBranch: true, RootRepoPath: rootRepo, BaseBranch: "main",
		Repos: []ports.WorkspaceProjectRepoConfig{{Name: "api", RelativePath: "api", RepoPath: childRepo, BaseBranch: "main"}},
	}
	first, err := ws.CreateWorkspaceProject(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	child := first.Worktrees[1]
	if child.CreationSHA == "" || child.CreationSHA != child.BaseSHA {
		t.Fatalf("initial child creation SHA = %q, diff base = %q", child.CreationSHA, child.BaseSHA)
	}
	if err := ws.forceDestroyPath(context.Background(), child.RepoPath, child.Path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(childRepo, "later.txt"), []byte("later\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, git, childRepo, "add", "later.txt")
	runGit(t, git, childRepo, "commit", "-m", "advance default after interrupted preparation")
	runGit(t, git, childRepo, "update-ref", "refs/remotes/origin/main", gitOutput(t, git, childRepo, "rev-parse", "main"))
	second, err := ws.CreateWorkspaceProject(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	recoveredChild := second.Worktrees[1]
	if recoveredChild.CreationSHA != "" {
		t.Fatalf("recovered creation SHA = %q, want unknown without a durable record", recoveredChild.CreationSHA)
	}
	if recoveredChild.BaseSHA == child.BaseSHA {
		t.Fatalf("recovered comparison base did not advance: %+v", recoveredChild)
	}
	if err := ws.DestroyWorkspaceProject(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	for _, row := range second.Worktrees {
		if err := ws.DeletePreparedBranch(context.Background(), ports.WorkspaceInfo{
			Branch: row.Branch, BaseSHA: row.CreationSHA, RepoPath: row.RepoPath, ProjectID: row.ProjectID,
		}); err != nil {
			t.Fatal(err)
		}
		if output, err := exec.Command(git, "-C", row.RepoPath, "show-ref", "--verify", "--quiet", "refs/heads/"+row.Branch).CombinedOutput(); err != nil {
			t.Fatalf("recovered branch %q was deleted without proof of its creation SHA: %v: %s", row.Branch, err, output)
		}
	}
}

func TestWorkspaceIntegrationCancelledPreparedChildAddRollsBackEveryRepo(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	rootRepo := setupOriginClone(t, git, filepath.Join(tmp, "root"))
	childRepo := setupOriginClone(t, git, filepath.Join(tmp, "child"))
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": rootRepo}})
	if err != nil {
		t.Fatal(err)
	}
	branch := "ao/prepared-cancelled-child"
	actualRun := ws.run
	ws.run = func(ctx context.Context, binary string, args ...string) ([]byte, error) {
		out, err := actualRun(ctx, binary, args...)
		if err == nil && strings.Contains(strings.Join(args, " "), "worktree add -b "+branch+" ") && strings.Contains(strings.Join(args, " "), childRepo) {
			return nil, context.Canceled
		}
		return out, err
	}
	info, err := ws.CreateWorkspaceProject(context.Background(), ports.WorkspaceProjectConfig{
		ProjectID: "proj", SessionID: "prepared-child", Kind: domain.KindWorker,
		Branch: branch, FreshBranch: true, RootRepoPath: rootRepo, BaseBranch: "main",
		Repos: []ports.WorkspaceProjectRepoConfig{{Name: "api", RelativePath: "api", RepoPath: childRepo, BaseBranch: "main"}},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("create error = %v, want cancellation", err)
	}
	if len(info.Worktrees) != 0 {
		t.Fatalf("clean rollback returned live worktrees: %+v", info.Worktrees)
	}
	for _, repo := range []string{rootRepo, childRepo} {
		if output, err := exec.Command(git, "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/"+branch).CombinedOutput(); err == nil {
			t.Fatalf("prepared branch remains in %q: %s", repo, output)
		}
	}
}

func TestWorkspaceIntegrationCancelledPreparedChildPreservesDirtyPartial(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	rootRepo := setupOriginClone(t, git, filepath.Join(tmp, "root"))
	childRepo := setupOriginClone(t, git, filepath.Join(tmp, "child"))
	if err := os.WriteFile(filepath.Join(rootRepo, ".gitignore"), []byte("api/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, git, rootRepo, "add", ".gitignore")
	runGit(t, git, rootRepo, "commit", "-m", "ignore child checkout")
	runGit(t, git, rootRepo, "push", "origin", "HEAD:main")
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": rootRepo}})
	if err != nil {
		t.Fatal(err)
	}
	branch := "ao/prepared-dirty-child"
	actualRun := ws.run
	ws.run = func(ctx context.Context, binary string, args ...string) ([]byte, error) {
		out, err := actualRun(ctx, binary, args...)
		joined := strings.Join(args, " ")
		if err == nil && strings.Contains(joined, "worktree add -b "+branch+" ") && strings.Contains(joined, childRepo) {
			path := filepath.Join(ws.managedRoot, "proj", "prepared-child", "api")
			if err := os.WriteFile(filepath.Join(path, "user.txt"), []byte("keep\n"), 0o644); err != nil {
				return nil, err
			}
			return nil, context.Canceled
		}
		return out, err
	}
	info, err := ws.CreateWorkspaceProject(context.Background(), ports.WorkspaceProjectConfig{
		ProjectID: "proj", SessionID: "prepared-child", Kind: domain.KindWorker,
		Branch: branch, FreshBranch: true, RootRepoPath: rootRepo, BaseBranch: "main",
		Repos: []ports.WorkspaceProjectRepoConfig{{Name: "api", RelativePath: "api", RepoPath: childRepo, BaseBranch: "main"}},
	})
	if !errors.Is(err, ports.ErrWorkspaceDirty) || len(info.Worktrees) != 2 || info.Worktrees[1].RepoName != "api" {
		t.Fatalf("dirty child rollback = (%+v, %v), want root and dirty child preserved", info, err)
	}
	if _, err := os.Stat(filepath.Join(info.Worktrees[1].Path, "user.txt")); err != nil {
		t.Fatalf("dirty child contents lost: %v", err)
	}
	if _, err := os.Stat(info.Worktrees[0].Path); err != nil {
		t.Fatalf("parent worktree removed after dirty child rollback: %v", err)
	}
	if info.Worktrees[1].CreationSHA == "" {
		t.Fatal("preserved child lost creation SHA cleanup fence")
	}
}

func TestWorkspaceIntegrationMixedWorkspaceContentReachesEverySessionKind(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	rootRepo := setupOriginClone(t, git, filepath.Join(tmp, "root"))
	childRepo := setupOriginClone(t, git, filepath.Join(tmp, "child"))
	if err := os.MkdirAll(filepath.Join(rootRepo, "docs"), 0o755); err != nil {
		t.Fatalf("create ordinary folder: %v", err)
	}
	if err := os.WriteFile(filepath.Join(rootRepo, "docs", "guide.md"), []byte("workspace guide\n"), 0o644); err != nil {
		t.Fatalf("write ordinary workspace file: %v", err)
	}
	runGit(t, git, rootRepo, "add", "docs/guide.md")
	runGit(t, git, rootRepo, "commit", "-m", "add workspace docs")
	runGit(t, git, rootRepo, "push", "origin", "HEAD:main")

	ws, err := New(Options{
		Binary: git, ManagedRoot: filepath.Join(tmp, "managed"),
		RepoResolver: StaticRepoResolver{"proj": rootRepo},
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	for _, kind := range []domain.SessionKind{domain.KindWorker, domain.KindOrchestrator} {
		t.Run(string(kind), func(t *testing.T) {
			info, err := ws.CreateWorkspaceProject(context.Background(), ports.WorkspaceProjectConfig{
				ProjectID: "proj", SessionID: domain.SessionID("mixed-" + kind), Kind: kind, Branch: "ao/mixed-" + string(kind),
				RootRepoPath: rootRepo,
				Repos: []ports.WorkspaceProjectRepoConfig{{
					Name: "api", RelativePath: "services/api", RepoPath: childRepo,
				}},
			})
			if err != nil {
				t.Fatalf("create mixed workspace: %v", err)
			}
			t.Cleanup(func() {
				if err := ws.DestroyWorkspaceProject(context.Background(), info); err != nil {
					t.Errorf("destroy mixed workspace: %v", err)
				}
			})
			for _, want := range []string{"docs/guide.md", "services/api/README.md"} {
				if _, err := os.Stat(filepath.Join(info.Root.Path, filepath.FromSlash(want))); err != nil {
					t.Errorf("%s session missing %s: %v", kind, want, err)
				}
			}
		})
	}
}

func TestWorkspaceIntegrationRestoreExistingBranchDoesNotResolveDefault(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	ws, err := New(Options{
		Binary:       git,
		ManagedRoot:  filepath.Join(tmp, "managed"),
		RepoResolver: StaticRepoResolver{"proj": repo},
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	cfg := ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/resume"}
	created, err := ws.Create(ctx, cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := ws.Destroy(ctx, created); err != nil {
		t.Fatalf("destroy: %v", err)
	}

	runGit(t, git, repo, "remote", "remove", "origin")
	_, err = ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "probe", Branch: "feature/new"})
	if !errors.Is(err, ports.ErrWorkspaceDefaultBranchUnresolved) {
		t.Fatalf("Create without remote metadata error = %v, want ErrWorkspaceDefaultBranchUnresolved", err)
	}

	cfg.Path = created.Path
	cfg.BaseRef = created.BaseRef
	restored, err := ws.Restore(ctx, cfg)
	if err != nil {
		t.Fatalf("restore existing session branch: %v", err)
	}
	if restored.Path != created.Path || restored.Branch != created.Branch || restored.BaseRef != created.BaseRef {
		t.Fatalf("restored = %#v, want path %q branch %q base ref %q", restored, created.Path, created.Branch, created.BaseRef)
	}
	if got := gitOutput(t, git, restored.Path, "rev-parse", "--abbrev-ref", "HEAD"); got != created.Branch {
		t.Fatalf("restored branch = %q, want %q", got, created.Branch)
	}
}

func TestWorkspaceIntegrationEnablesPerWorktreeConfig(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	root := filepath.Join(tmp, "managed")
	ws, err := New(Options{Binary: git, ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	info, err := ws.Create(context.Background(), ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/config"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer func() { _ = ws.Destroy(context.Background(), info) }()

	if got := strings.TrimSpace(string(outputGit(t, git, repo, "config", "--get", "extensions.worktreeConfig"))); got != "true" {
		t.Fatalf("extensions.worktreeConfig = %q, want true", got)
	}
	runGit(t, git, info.Path, "config", "--worktree", "ao.worker-setting", "session-only")
	if got := strings.TrimSpace(string(outputGit(t, git, info.Path, "config", "--get", "ao.worker-setting"))); got != "session-only" {
		t.Fatalf("worktree setting = %q, want session-only", got)
	}
	if _, err := exec.Command(git, "-C", repo, "config", "--get", "ao.worker-setting").Output(); err == nil {
		t.Fatal("worktree-scoped setting leaked into the human checkout")
	}
}

func TestWorkspaceIntegrationDestroyRefusesLockedWorktree(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	root := filepath.Join(tmp, "managed")
	ws, err := New(Options{Binary: git, ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	info, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/lock"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	runGit(t, git, repo, "worktree", "lock", info.Path)

	err = ws.Destroy(ctx, info)
	if !errors.Is(err, ports.ErrWorkspaceLocked) {
		t.Fatalf("destroy locked error = %v, want ports.ErrWorkspaceLocked", err)
	}
	if _, statErr := os.Stat(filepath.Join(info.Path, "README.md")); statErr != nil {
		t.Fatalf("locked worktree was not preserved: %v", statErr)
	}

	runGit(t, git, repo, "worktree", "unlock", info.Path)
	if err := ws.Destroy(ctx, info); err != nil {
		t.Fatalf("destroy after unlock: %v", err)
	}
}

func TestWorkspaceIntegrationDestroyDirtyWorktree(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	root := filepath.Join(tmp, "managed")
	ws, err := New(Options{Binary: git, ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	info, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/dirty"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	hookDir := filepath.Join(info.Path, ".codex")
	if err := os.MkdirAll(hookDir, 0o750); err != nil {
		t.Fatalf("mkdir hook dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hookDir, "hooks.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write hooks.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(hookDir, ".gitignore"), []byte(".gitignore\nhooks.json\n"), 0o600); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	wip := filepath.Join(info.Path, "wip.txt")
	if err := os.WriteFile(wip, []byte("uncommitted\n"), 0o600); err != nil {
		t.Fatalf("write wip: %v", err)
	}
	err = ws.Destroy(ctx, info)
	if !errors.Is(err, ports.ErrWorkspaceDirty) {
		t.Fatalf("destroy dirty error = %v, want ports.ErrWorkspaceDirty", err)
	}
	if _, statErr := os.Stat(wip); statErr != nil {
		t.Fatalf("dirty worktree was not preserved: %v", statErr)
	}

	if err := os.Remove(wip); err != nil {
		t.Fatalf("remove wip: %v", err)
	}
	if err := ws.Destroy(ctx, info); err != nil {
		t.Fatalf("destroy with ignored-only files: %v", err)
	}
	if _, err := os.Stat(info.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("path after destroy stat err = %v, want not exist", err)
	}
}

func TestWorkspaceIntegrationDestroyDeletesOnlyThatSessionsFileSnapshots(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	ended, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/ended"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	dirty, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess-2", Branch: "feature/dirty"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := filesnapshot.Capture(ctx, ended.Path, filesnapshot.Ref("sess", "turn-1"), filesnapshot.Ref("sess", "turn-2")); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if err := filesnapshot.Capture(ctx, dirty.Path, filesnapshot.Ref("sess-2", "turn-1")); err != nil {
		t.Fatalf("capture: %v", err)
	}

	if err := ws.Destroy(ctx, ended); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dirty.Path, "wip.txt"), []byte("uncommitted\n"), 0o600); err != nil {
		t.Fatalf("write wip: %v", err)
	}
	if err := ws.Destroy(ctx, dirty); !errors.Is(err, ports.ErrWorkspaceDirty) {
		t.Fatalf("destroy dirty = %v, want ports.ErrWorkspaceDirty", err)
	}

	if got := gitOutput(t, git, repo, "for-each-ref", "--format=%(refname)", "refs/ao/snapshots/"); got != filesnapshot.Ref("sess-2", "turn-1") {
		t.Fatalf("snapshot refs = %q, want only the surviving session's", got)
	}
}

func TestWorkspaceIntegrationCreateDeletesFileSnapshotsOfWorktreesThatAreGone(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	live, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/live"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	gone, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess-2", Branch: "feature/gone"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := filesnapshot.Capture(ctx, live.Path, filesnapshot.Ref("sess", "turn-1")); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if err := filesnapshot.Capture(ctx, gone.Path, filesnapshot.Ref("sess-2", "turn-1")); err != nil {
		t.Fatalf("capture: %v", err)
	}
	gitOutput(t, git, repo, "worktree", "remove", "--force", gone.Path)

	if _, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess-3", Branch: "feature/next"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := gitOutput(t, git, repo, "for-each-ref", "--format=%(refname)", "refs/ao/snapshots/"); got != filesnapshot.Ref("sess", "turn-1") {
		t.Fatalf("snapshot refs = %q, want only the session whose worktree remains", got)
	}
}

func TestWorkspaceIntegrationRestoreRecreatesSiblingsIndependently(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	root := filepath.Join(tmp, "managed")
	ws, err := New(Options{Binary: git, ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()

	infoA, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess-a", Branch: "child-a"})
	if err != nil {
		t.Fatalf("create A: %v", err)
	}
	infoB, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess-b", Branch: "child-b"})
	if err != nil {
		t.Fatalf("create B: %v", err)
	}

	if err := os.RemoveAll(infoA.Path); err != nil {
		t.Fatalf("remove A dir: %v", err)
	}
	if err := os.RemoveAll(infoB.Path); err != nil {
		t.Fatalf("remove B dir: %v", err)
	}

	restoredA, err := ws.Restore(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess-a", Branch: "wrong-branch-a", Path: infoA.Path})
	if err != nil {
		t.Fatalf("restore A: %v", err)
	}
	if restoredA.Branch != "child-a" {
		t.Fatalf("restored A branch = %q, want child-a", restoredA.Branch)
	}

	restoredB, err := ws.Restore(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess-b", Branch: "wrong-branch-b", Path: infoB.Path})
	if err != nil {
		t.Fatalf("restore B: %v", err)
	}
	if restoredB.Branch != "child-b" {
		t.Fatalf("restored B branch = %q, want child-b (A's restore must not have wiped B's registration)", restoredB.Branch)
	}

	if err := ws.Destroy(ctx, restoredA); err != nil {
		t.Fatalf("destroy A: %v", err)
	}
	if err := ws.Destroy(ctx, restoredB); err != nil {
		t.Fatalf("destroy B: %v", err)
	}
}

func TestWorkspaceIntegrationRestoreLockedMissingWorktreeIsTypedError(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	root := filepath.Join(tmp, "managed")
	ws, err := New(Options{Binary: git, ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	info, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess-locked", Branch: "child-locked"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	runGit(t, git, repo, "worktree", "lock", info.Path)
	if err := os.RemoveAll(info.Path); err != nil {
		t.Fatalf("remove dir: %v", err)
	}

	var attemptedAdd bool
	ws.run = func(ctx context.Context, binary string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "worktree add") && strings.Contains(joined, info.Path) {
			attemptedAdd = true
		}
		return runCommand(ctx, binary, args...)
	}

	_, restoreErr := ws.Restore(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess-locked", Branch: "child-locked", Path: info.Path})
	if !errors.Is(restoreErr, ports.ErrWorkspaceLocked) {
		t.Fatalf("restore locked-missing error = %v, want ports.ErrWorkspaceLocked", restoreErr)
	}
	if attemptedAdd {
		t.Fatal("Restore attempted `git worktree add` at a locked, missing worktree path")
	}

	runGit(t, git, repo, "worktree", "unlock", info.Path)
}

func TestWorkspaceIntegrationRestoreDoesNotDestroyWorktreeRecreatedMidRecovery(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	root := filepath.Join(tmp, "managed")
	ws, err := New(Options{Binary: git, ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	info, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "child"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := os.RemoveAll(info.Path); err != nil {
		t.Fatalf("remove dir: %v", err)
	}

	sentinel := filepath.Join(info.Path, "AGENT_WORK.txt")
	raced := false
	ws.run = func(ctx context.Context, binary string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		mutatesRegistration := strings.Contains(joined, "worktree add") ||
			strings.Contains(joined, "worktree remove") ||
			strings.Contains(joined, "worktree prune")
		if !raced && mutatesRegistration {
			raced = true
			runGit(t, git, repo, "worktree", "add", "--force", info.Path, "child")
			if err := os.WriteFile(sentinel, []byte("uncommitted agent work\n"), 0o644); err != nil {
				t.Fatalf("write sentinel: %v", err)
			}
		}
		return runCommand(ctx, binary, args...)
	}

	restored, restoreErr := ws.Restore(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "child", Path: info.Path})
	if !raced {
		t.Fatal("Restore issued no registration-mutating git command, so the race window was never forced")
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("uncommitted work in the concurrently recreated worktree did not survive stale-registration recovery: %v", err)
	}
	if restoreErr == nil && restored.Path != info.Path {
		t.Fatalf("restored = %#v, want a handle to the live worktree at %q", restored, info.Path)
	}
}

func TestWorkspaceIntegrationAddNewBranchRecoversStaleRegistration(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	root := filepath.Join(tmp, "managed")
	ws, err := New(Options{Binary: git, ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()

	info, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "child-old"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := os.RemoveAll(info.Path); err != nil {
		t.Fatalf("remove dir: %v", err)
	}

	if err := ws.addNewBranchWorktree(ctx, repo, "child-new", info.Path, "origin/main", false); err != nil {
		t.Fatalf("addNewBranchWorktree over a stale registration: %v", err)
	}

	if _, err := os.Stat(filepath.Join(info.Path, "README.md")); err != nil {
		t.Fatalf("recovered worktree was not materialized: %v", err)
	}
	out, err := exec.Command(git, "-C", info.Path, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse recovered worktree: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "child-new" {
		t.Fatalf("recovered worktree branch = %q, want child-new", got)
	}
	head, err := exec.Command(git, "-C", repo, "rev-parse", "refs/heads/child-new").Output()
	if err != nil {
		t.Fatalf("rev-parse child-new: %v", err)
	}
	base, err := exec.Command(git, "-C", repo, "rev-parse", "origin/main").Output()
	if err != nil {
		t.Fatalf("rev-parse origin/main: %v", err)
	}
	if strings.TrimSpace(string(head)) != strings.TrimSpace(string(base)) {
		t.Fatalf("child-new = %s, want origin/main %s", head, base)
	}
}

func TestWorkspaceIntegrationAutoRejectsUnmarkedRemotelessRepo(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "unmarked")
	run(t, git, "init", "-b", "main", repo)
	runGit(t, git, repo, "config", "core.autocrlf", "false")
	runGit(t, git, repo, "config", "user.email", "ao@example.com")
	runGit(t, git, repo, "config", "user.name", "Ao Agents")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	runGit(t, git, repo, "add", "README.md")
	runGit(t, git, repo, "commit", "-m", "seed")
	runGit(t, git, repo, "switch", "-c", "feature/temporary")

	root := filepath.Join(tmp, "managed")
	ws, err := New(Options{Binary: git, ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	_, err = ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "ao/proj-1"})
	if !errors.Is(err, ports.ErrWorkspaceDefaultBranchUnresolved) {
		t.Fatalf("Create error = %v, want ErrWorkspaceDefaultBranchUnresolved", err)
	}
	if !strings.Contains(err.Error(), "remote set-head") || !strings.Contains(err.Error(), repo) {
		t.Fatalf("Create error = %v, want repository-specific remote HEAD remediation", err)
	}
	if out, err := exec.Command(git, "-C", repo, "show-ref", "--verify", "--quiet", "refs/heads/ao/proj-1").CombinedOutput(); err == nil {
		t.Fatalf("unresolved auto selection leaked a session branch: %s", out)
	}
}

func TestWorkspaceIntegrationAutoUsesAOInitializedRemotelessDefault(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	run(t, git, "init", "-b", "main", repo)
	runGit(t, git, repo, "config", "core.autocrlf", "false")
	runGit(t, git, repo, "config", "user.email", "ao@example.com")
	runGit(t, git, repo, "config", "user.name", "Ao Agents")
	runGit(t, git, repo, "config", "ao.defaultBranch", "main")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	runGit(t, git, repo, "add", "README.md")
	runGit(t, git, repo, "commit", "-m", "seed")
	runGit(t, git, repo, "switch", "-c", "feature/temporary")

	ws, err := New(Options{
		Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo},
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	info, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "ao/proj-1"})
	if err != nil {
		t.Fatalf("create in AO-initialized repo: %v", err)
	}
	if got := gitOutput(t, git, info.Path, "merge-base", "HEAD", "refs/heads/main"); got != gitOutput(t, git, repo, "rev-parse", "refs/heads/main") {
		t.Fatalf("worktree base = %s, want AO-recorded main", got)
	}
	if err := ws.Destroy(ctx, info); err != nil {
		t.Fatalf("destroy: %v", err)
	}
}

func TestWorkspaceIntegrationAutoUsesRequestedRemoteBranchWithoutRemoteHead(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	branch := "feature/resume"
	sha := gitOutput(t, git, repo, "rev-parse", "HEAD")
	runGit(t, git, repo, "update-ref", "refs/remotes/origin/"+branch, sha)
	runGit(t, git, repo, "symbolic-ref", "--delete", "refs/remotes/origin/HEAD")
	origin := gitOutput(t, git, repo, "remote", "get-url", "origin")
	runGit(t, git, origin, "update-ref", "--no-deref", "HEAD", sha)

	ws, err := New(Options{
		Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo},
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	info, err := ws.Create(context.Background(), ports.WorkspaceConfig{
		ProjectID: "proj", SessionID: "sess", Branch: branch,
	})
	if err != nil {
		t.Fatalf("create from requested remote branch: %v", err)
	}
	if info.BaseRef != "refs/remotes/origin/"+branch {
		t.Fatalf("base ref = %q, want requested origin branch", info.BaseRef)
	}
	if got := gitOutput(t, git, info.Path, "rev-parse", "HEAD"); got != sha {
		t.Fatalf("worktree HEAD = %s, want requested remote SHA %s", got, sha)
	}
}

func TestWorkspaceIntegrationRequestedRemoteBranchKeepsDefaultComparisonBase(t *testing.T) {
	git := requireGit(t)
	for _, tc := range []struct {
		name        string
		baseBranch  string
		wantBaseRef string
	}{
		{name: "automatic default", wantBaseRef: "refs/remotes/origin/main"},
		{name: "explicit default", baseBranch: "main", wantBaseRef: "origin/main"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			repo := setupOriginClone(t, git, tmp)
			branch := "feature/resume"
			defaultSHA, featureSHA := pushRemoteBranchCommit(t, git, repo, "main", branch)

			ws, err := New(Options{
				Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo},
			})
			if err != nil {
				t.Fatalf("new: %v", err)
			}
			info, err := ws.Create(context.Background(), ports.WorkspaceConfig{
				ProjectID: "proj", SessionID: "sess", Branch: branch, BaseBranch: tc.baseBranch,
			})
			if err != nil {
				t.Fatalf("create from requested remote branch: %v", err)
			}
			if info.BaseRef != tc.wantBaseRef {
				t.Fatalf("base ref = %q, want repository default %q", info.BaseRef, tc.wantBaseRef)
			}
			if got := gitOutput(t, git, info.Path, "rev-parse", "HEAD"); got != featureSHA {
				t.Fatalf("worktree HEAD = %s, want requested remote branch %s", got, featureSHA)
			}
			if got := gitOutput(t, git, info.Path, "merge-base", "HEAD", info.BaseRef); got != defaultSHA {
				t.Fatalf("comparison merge-base = %s, want repository default %s", got, defaultSHA)
			}
		})
	}
}

func TestWorkspaceIntegrationRemotelessRootUsesImportedDefaultBranch(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	rootRepo := setupOriginCloneOnBranch(t, git, filepath.Join(tmp, "root"), "trunk")
	gitOutput(t, git, rootRepo, "remote", "remove", "origin")
	childRepo := setupOriginCloneOnBranch(t, git, filepath.Join(tmp, "child"), "dev")
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": rootRepo}})
	if err != nil {
		t.Fatal(err)
	}
	cfg := ports.WorkspaceProjectConfig{
		ProjectID: "proj", SessionID: "orch", Kind: "orchestrator", Branch: "ao/proj-orch",
		RootRepoPath: rootRepo,
		BaseBranch:   "trunk",
		Repos:        []ports.WorkspaceProjectRepoConfig{{Name: "api", RelativePath: "api", RepoPath: childRepo}},
	}
	info, err := ws.CreateWorkspaceProject(context.Background(), cfg)
	if err != nil {
		t.Fatalf("spawn with imported root default: %v", err)
	}
	defer func() {
		if err := ws.DestroyWorkspaceProject(context.Background(), info); err != nil {
			t.Errorf("destroy: %v", err)
		}
	}()
	if len(info.Worktrees) != 2 {
		t.Fatalf("worktrees = %d, want root and child", len(info.Worktrees))
	}
	for _, wt := range info.Worktrees {
		want := gitOutput(t, git, rootRepo, "rev-parse", "trunk")
		if wt.RepoName == "api" {
			want = gitOutput(t, git, childRepo, "rev-parse", "origin/dev")
		}
		if got := gitOutput(t, git, wt.Path, "rev-parse", "HEAD"); got != want {
			t.Errorf("%s HEAD = %s, want %s", wt.RepoName, got, want)
		}
	}
}

func TestWorkspaceIntegrationWorkspaceProjectInfersPerRepoDefaultBranches(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	rootRepo := setupOriginCloneOnBranch(t, git, filepath.Join(tmp, "root"), "trunk")
	devChildRepo := setupOriginCloneOnBranch(t, git, filepath.Join(tmp, "dev-child"), "dev")
	mainChildRepo := setupOriginClone(t, git, filepath.Join(tmp, "main-child"))
	devDefaultSHA, devSessionSHA := pushRemoteBranchCommit(t, git, devChildRepo, "dev", "ao/proj-1")

	root := filepath.Join(tmp, "managed")
	ws, err := New(Options{Binary: git, ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": rootRepo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	info, err := ws.CreateWorkspaceProject(context.Background(), ports.WorkspaceProjectConfig{
		ProjectID:    "proj",
		SessionID:    "sess",
		Kind:         "worker",
		Branch:       "ao/proj-1",
		RootRepoPath: rootRepo,
		Repos: []ports.WorkspaceProjectRepoConfig{
			{
				Name:         "api",
				RelativePath: "services/api",
				RepoPath:     devChildRepo,
			},
			{
				Name:         "web",
				RelativePath: "apps/web",
				RepoPath:     mainChildRepo,
			},
		},
	})
	if err != nil {
		t.Fatalf("create workspace project: %v", err)
	}
	if len(info.Worktrees) != 3 {
		t.Fatalf("worktrees = %d, want root and two children: %#v", len(info.Worktrees), info.Worktrees)
	}
	wantRefs := map[string]string{
		"__root__": "refs/heads/trunk",
		"api":      "refs/remotes/origin/dev",
		"web":      "refs/remotes/origin/main",
	}
	for _, wt := range info.Worktrees {
		if wt.BaseRef != wantRefs[wt.RepoName] {
			t.Fatalf("worktree %q base ref = %q, want %q", wt.RepoName, wt.BaseRef, wantRefs[wt.RepoName])
		}
		if got := gitOutput(t, git, wt.RepoPath, "rev-parse", wt.BaseRef); wt.BaseSHA != got {
			t.Fatalf("worktree %q base SHA = %q, want default SHA %q", wt.RepoName, wt.BaseSHA, got)
		}
	}
	devChildPath := filepath.Join(info.Root.Path, "services", "api")
	if _, err := os.Stat(filepath.Join(devChildPath, "README.md")); err != nil {
		t.Fatalf("dev child worktree missing seed file: %v", err)
	}
	devChildHead := gitOutput(t, git, devChildRepo, "rev-parse", "refs/heads/ao/proj-1")
	devChildBase := gitOutput(t, git, devChildRepo, "rev-parse", "origin/dev")
	if devChildBase != devDefaultSHA {
		t.Fatalf("dev child default = %s, want original default %s", devChildBase, devDefaultSHA)
	}
	if devChildHead != devSessionSHA {
		t.Fatalf("dev child branch seed = %s, want requested remote branch %s", devChildHead, devSessionSHA)
	}
	mainChildHead := gitOutput(t, git, mainChildRepo, "rev-parse", "refs/heads/ao/proj-1")
	mainChildBase := gitOutput(t, git, mainChildRepo, "rev-parse", "origin/main")
	if mainChildHead != mainChildBase {
		t.Fatalf("main child branch base = %s, want origin/main %s", mainChildHead, mainChildBase)
	}
	rootHead := gitOutput(t, git, rootRepo, "rev-parse", "refs/heads/ao/proj-1")
	rootBase := gitOutput(t, git, rootRepo, "rev-parse", "origin/trunk")
	if rootHead != rootBase {
		t.Fatalf("root branch base = %s, want origin/trunk %s", rootHead, rootBase)
	}
	if err := ws.DestroyWorkspaceProject(context.Background(), info); err != nil {
		t.Fatalf("destroy workspace project: %v", err)
	}
}

func TestWorkspaceIntegrationWorkspaceProjectCreateRecoversExistingPreparation(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	rootRepo := setupOriginClone(t, git, filepath.Join(tmp, "root"))
	childRepo := setupOriginClone(t, git, filepath.Join(tmp, "child"))
	ws, err := New(Options{
		Binary: git, ManagedRoot: filepath.Join(tmp, "managed"),
		RepoResolver: StaticRepoResolver{"proj": rootRepo},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := ports.WorkspaceProjectConfig{
		ProjectID: "proj", SessionID: "sess", Kind: "worker", Branch: "ao/prepared",
		RootRepoPath: rootRepo,
		Repos: []ports.WorkspaceProjectRepoConfig{{
			Name: "child", RelativePath: "child", RepoPath: childRepo,
		}},
	}
	first, err := ws.CreateWorkspaceProject(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	child := first.Worktrees[1]
	if err := ws.forceDestroyPath(context.Background(), child.RepoPath, child.Path); err != nil {
		t.Fatalf("simulate interrupted child creation: %v", err)
	}
	second, err := ws.CreateWorkspaceProject(context.Background(), cfg)
	if err != nil {
		t.Fatalf("recover existing preparation: %v", err)
	}
	if second.Root.Path != first.Root.Path || second.Root.Branch != first.Root.Branch {
		t.Fatalf("recovered root = %+v, want %+v", second.Root, first.Root)
	}
	if len(second.Worktrees) != len(first.Worktrees) {
		t.Fatalf("recovered worktrees = %d, want %d", len(second.Worktrees), len(first.Worktrees))
	}
	if _, err := os.Stat(child.Path); err != nil {
		t.Fatalf("missing child was not recreated: %v", err)
	}
	if err := ws.DestroyWorkspaceProject(context.Background(), second); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceIntegrationDestroyWorkspaceProjectPreservesDirtyChild(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	rootRepo := setupOriginClone(t, git, filepath.Join(tmp, "root"))
	childRepo := setupOriginClone(t, git, filepath.Join(tmp, "child"))
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": rootRepo}})
	if err != nil {
		t.Fatal(err)
	}
	info, err := ws.CreateWorkspaceProject(context.Background(), ports.WorkspaceProjectConfig{
		ProjectID: "proj", SessionID: "sess", Kind: "worker", Branch: "ao/dirty-child",
		RootRepoPath: rootRepo,
		Repos:        []ports.WorkspaceProjectRepoConfig{{Name: "child", RelativePath: "child", RepoPath: childRepo}},
	})
	if err != nil {
		t.Fatal(err)
	}
	dirtyFile := filepath.Join(info.Worktrees[1].Path, "user-change.txt")
	if err := os.WriteFile(dirtyFile, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ws.DestroyWorkspaceProject(context.Background(), info); !errors.Is(err, ports.ErrWorkspaceDirty) {
		t.Fatalf("destroy dirty workspace project = %v, want ErrWorkspaceDirty", err)
	}
	if content, err := os.ReadFile(dirtyFile); err != nil || string(content) != "keep me" {
		t.Fatalf("dirty child was lost: %q, %v", content, err)
	}
}

func TestWorkspaceIntegrationWorkspaceProjectCopiesAssetsAndCleansSessionCopy(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	rootRepo := setupOriginClone(t, git, filepath.Join(tmp, "root"))
	childRepo := setupOriginClone(t, git, filepath.Join(rootRepo, "api"))
	asset := filepath.Join(rootRepo, "notes")
	if err := os.MkdirAll(filepath.Join(asset, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	sourceFile := filepath.Join(asset, "nested", "context.txt")
	if err := os.WriteFile(sourceFile, []byte("source context"), 0o640); err != nil {
		t.Fatal(err)
	}
	runGit(t, git, rootRepo, "add", "notes/nested/context.txt")
	runGit(t, git, rootRepo, "commit", "-m", "track workspace notes")
	runGit(t, git, rootRepo, "push", "origin", "main")
	if err := os.Mkdir(filepath.Join(asset, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(asset, ".git", "secret"), []byte("never copy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("nested/context.txt", filepath.Join(asset, "latest")); err != nil {
		t.Fatal(err)
	}

	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": rootRepo}})
	if err != nil {
		t.Fatal(err)
	}
	info, err := ws.CreateWorkspaceProject(context.Background(), ports.WorkspaceProjectConfig{
		ProjectID: "proj", SessionID: "sess", Kind: "worker", Branch: "ao/assets",
		RootRepoPath: rootRepo,
		Repos:        []ports.WorkspaceProjectRepoConfig{{Name: "api", RelativePath: "api", RepoPath: childRepo}},
		Assets:       []ports.WorkspaceProjectAssetConfig{{RelativePath: "notes", SourcePath: asset}},
	})
	if err != nil {
		t.Fatalf("create workspace project: %v", err)
	}
	copied, err := os.ReadFile(filepath.Join(info.Root.Path, "notes", "nested", "context.txt"))
	if err != nil || string(copied) != "source context" {
		t.Fatalf("copied asset = %q, %v", copied, err)
	}
	if target, err := os.Readlink(filepath.Join(info.Root.Path, "notes", "latest")); err != nil || target != "nested/context.txt" {
		t.Fatalf("copied symlink = %q, %v", target, err)
	}
	if _, err := os.Stat(filepath.Join(info.Root.Path, "notes", ".git")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("asset .git metadata was copied: %v", err)
	}
	if len(info.Worktrees) != 2 || info.Worktrees[1].RepoName != "api" {
		t.Fatalf("worktrees = %#v, want root and independent api child", info.Worktrees)
	}
	if _, err := os.Stat(filepath.Join(info.Root.Path, "api", "README.md")); err != nil {
		t.Fatalf("child worktree missing: %v", err)
	}
	if err := ws.DestroyWorkspaceProject(context.Background(), info); !errors.Is(err, ports.ErrWorkspaceDirty) {
		t.Fatalf("destroy workspace project with untracked asset = %v, want ErrWorkspaceDirty", err)
	}
	if _, err := os.Lstat(filepath.Join(info.Root.Path, "notes", "latest")); err != nil {
		t.Fatalf("untracked asset was deleted: %v", err)
	}
	if err := os.Remove(filepath.Join(info.Root.Path, "notes", "latest")); err != nil {
		t.Fatal(err)
	}
	if err := ws.DestroyWorkspaceProject(context.Background(), info); err != nil {
		t.Fatalf("destroy clean workspace project: %v", err)
	}
	if _, err := os.Stat(info.Root.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("session copy still exists after cleanup: %v", err)
	}
	if got, err := os.ReadFile(sourceFile); err != nil || string(got) != "source context" {
		t.Fatalf("source asset changed during lifecycle: %q, %v", got, err)
	}
}

func TestWorkspaceIntegrationWorkspaceProjectRepairsRemotelessRootDefault(t *testing.T) {
	git := requireGit(t)
	for _, kind := range []domain.SessionKind{domain.KindWorker, domain.KindOrchestrator} {
		t.Run(string(kind), func(t *testing.T) {
			tmp := t.TempDir()
			rootRepo := filepath.Join(tmp, "root")
			run(t, git, "init", "-b", "trunk", rootRepo)
			runGit(t, git, rootRepo, "config", "user.email", "ao@example.com")
			runGit(t, git, rootRepo, "config", "user.name", "AO Test")
			if err := os.WriteFile(filepath.Join(rootRepo, "README.md"), []byte("root\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			runGit(t, git, rootRepo, "add", "README.md")
			runGit(t, git, rootRepo, "commit", "-m", "initial")
			childRepo := setupOriginClone(t, git, filepath.Join(rootRepo, "api"))

			ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": rootRepo}})
			if err != nil {
				t.Fatal(err)
			}
			info, err := ws.CreateWorkspaceProject(context.Background(), ports.WorkspaceProjectConfig{
				ProjectID: "proj", SessionID: domain.SessionID("sess-" + string(kind)), Kind: kind, Branch: "ao/local-root-" + string(kind),
				RootRepoPath: rootRepo,
				Repos:        []ports.WorkspaceProjectRepoConfig{{Name: "api", RelativePath: "api", RepoPath: childRepo}},
			})
			if err != nil {
				t.Fatalf("create workspace project: %v", err)
			}
			defer func() { _ = ws.DestroyWorkspaceProject(context.Background(), info) }()
			if info.Root.BaseRef != "refs/heads/trunk" {
				t.Fatalf("root BaseRef = %q, want local trunk", info.Root.BaseRef)
			}
			if got := gitOutput(t, git, rootRepo, "config", "--local", "--get", "ao.defaultBranch"); got != "trunk" {
				t.Fatalf("recorded root default = %q, want trunk", got)
			}
			if len(info.Worktrees) != 2 || info.Worktrees[1].RepoName != "api" {
				t.Fatalf("worktrees = %#v, want repaired root and api child", info.Worktrees)
			}
		})
	}
}

func TestWorkspaceIntegrationWorkspaceProjectAssetCopyFailureRollsBackRoot(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	rootRepo := setupOriginClone(t, git, filepath.Join(tmp, "root"))
	asset := filepath.Join(rootRepo, "notes")
	if err := os.Mkdir(asset, 0o755); err != nil {
		t.Fatal(err)
	}
	conflictingFile := filepath.Join(rootRepo, "conflict.txt")
	if err := os.WriteFile(conflictingFile, []byte("conflict"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": rootRepo}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ws.CreateWorkspaceProject(context.Background(), ports.WorkspaceProjectConfig{
		ProjectID: "proj", SessionID: "sess", Kind: "worker", Branch: "ao/assets-fail",
		RootRepoPath: rootRepo,
		Assets: []ports.WorkspaceProjectAssetConfig{
			{RelativePath: "notes", SourcePath: asset},
			{RelativePath: "notes", SourcePath: conflictingFile},
		},
	})
	if err == nil {
		t.Fatal("expected conflicting asset destination to fail")
	}
	rootPath := filepath.Join(tmp, "managed", "proj", "worker", "sess")
	if _, statErr := os.Stat(rootPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("root worktree was not rolled back: %v", statErr)
	}
}

func TestFetchDefaultBranchRefreshesRemoteTrackingRef(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	root := filepath.Join(tmp, "managed")
	ws, err := New(Options{Binary: git, ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	runGit(t, git, repo, "config", "remote.origin.fetch", "+refs/heads/restricted-only:refs/remotes/origin/restricted-only")
	originURL := gitOutput(t, git, repo, "remote", "get-url", "origin")
	updater := filepath.Join(tmp, "updater")
	run(t, git, "clone", originURL, updater)
	runGit(t, git, updater, "config", "user.email", "ao@example.com")
	runGit(t, git, updater, "config", "user.name", "Ao Agents")
	runGit(t, git, updater, "checkout", "-B", "main", "origin/main")
	if err := os.WriteFile(filepath.Join(updater, "fresh.txt"), []byte("fresh\n"), 0o644); err != nil {
		t.Fatalf("write fresh file: %v", err)
	}
	runGit(t, git, updater, "add", "fresh.txt")
	runGit(t, git, updater, "commit", "-m", "fresh")
	freshMain := gitOutput(t, git, updater, "rev-parse", "HEAD")
	runGit(t, git, updater, "push", "origin", "HEAD:main")

	staleOriginMain := gitOutput(t, git, repo, "rev-parse", "refs/remotes/origin/main")
	if staleOriginMain == freshMain {
		t.Fatal("test setup did not leave local origin/main stale")
	}
	target, err := ws.ResolveDefaultBranch(context.Background(), repo, "main")
	if err != nil {
		t.Fatalf("resolve default branch: %v", err)
	}
	if target.BaseRef != "refs/remotes/origin/main" {
		t.Fatalf("base ref = %q, want refs/remotes/origin/main", target.BaseRef)
	}
	if err := ws.FetchDefaultBranch(context.Background(), repo, target); err != nil {
		t.Fatalf("fetch default branch: %v", err)
	}
	if got := gitOutput(t, git, repo, "rev-parse", "refs/remotes/origin/main"); got != freshMain {
		t.Fatalf("origin/main = %s, want refreshed %s", got, freshMain)
	}
}

func TestResolveDefaultBranchKeepsSlashBranchOnOriginWithoutMatchingRemote(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	target, err := ws.ResolveDefaultBranch(context.Background(), repo, "release/2026")
	if err != nil {
		t.Fatalf("resolve slash branch: %v", err)
	}
	want := ports.WorkspaceDefaultBranch{Remote: "origin", Branch: "release/2026", BaseRef: "refs/remotes/origin/release/2026"}
	if target != want {
		t.Fatalf("target = %#v, want %#v", target, want)
	}
}

func TestResolveDefaultBranchFallsBackToLocalHeadWhenExplicitBranchHasNoRemoteTrackingRef(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	run(t, git, "init", "-b", "main", repo)
	runGit(t, git, repo, "config", "user.email", "ao@example.com")
	runGit(t, git, repo, "config", "user.name", "Ao Agents")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	runGit(t, git, repo, "add", "README.md")
	runGit(t, git, repo, "commit", "-m", "seed")
	runGit(t, git, repo, "remote", "add", "origin", "https://github.com/example/missing.git")

	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	target, err := ws.ResolveDefaultBranch(context.Background(), repo, "main")
	if err != nil {
		t.Fatalf("resolve default branch: %v", err)
	}
	want := ports.WorkspaceDefaultBranch{Remote: "origin", Branch: "main", BaseRef: "refs/heads/main"}
	if target != want {
		t.Fatalf("target = %#v, want %#v", target, want)
	}
	if err := ws.FetchDefaultBranch(context.Background(), repo, target); err != nil {
		t.Fatalf("fetch default branch local fallback: %v", err)
	}
}

func TestWorkspaceIntegrationRefreshesInferredChildBaseBeforeMaterialization(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	rootRepo := setupOriginClone(t, git, filepath.Join(tmp, "root"))
	childRepo := setupOriginCloneOnBranch(t, git, filepath.Join(tmp, "child"), "dev")
	originURL := gitOutput(t, git, childRepo, "remote", "get-url", "origin")
	updater := filepath.Join(tmp, "child-updater")
	run(t, git, "clone", originURL, updater)
	runGit(t, git, updater, "config", "user.email", "ao@example.com")
	runGit(t, git, updater, "config", "user.name", "Ao Agents")
	runGit(t, git, updater, "checkout", "-B", "dev", "origin/dev")
	if err := os.WriteFile(filepath.Join(updater, "fresh-child.txt"), []byte("fresh\n"), 0o644); err != nil {
		t.Fatalf("write fresh child file: %v", err)
	}
	runGit(t, git, updater, "add", "fresh-child.txt")
	runGit(t, git, updater, "commit", "-m", "advance child dev")
	freshChild := gitOutput(t, git, updater, "rev-parse", "HEAD")
	runGit(t, git, updater, "push", "origin", "HEAD:dev")

	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": rootRepo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	childTarget, err := ws.ResolveDefaultBranch(context.Background(), childRepo, "")
	if err != nil {
		t.Fatalf("resolve inferred child default: %v", err)
	}
	if got, want := childTarget.BaseRef, "refs/remotes/origin/dev"; got != want {
		t.Fatalf("inferred child base ref = %q, want %q", got, want)
	}
	if err := ws.FetchDefaultBranch(context.Background(), childRepo, childTarget); err != nil {
		t.Fatalf("refresh inferred child default: %v", err)
	}

	info, err := ws.CreateWorkspaceProject(context.Background(), ports.WorkspaceProjectConfig{
		ProjectID:    "proj",
		SessionID:    "sess-fresh-child",
		Kind:         "worker",
		Branch:       "ao/proj-fresh-child",
		RootRepoPath: rootRepo,
		BaseBranch:   "main",
		Repos: []ports.WorkspaceProjectRepoConfig{{
			Name:         "api",
			RelativePath: "services/api",
			RepoPath:     childRepo,
			BaseRef:      childTarget.BaseRef,
		}},
	})
	if err != nil {
		t.Fatalf("create workspace project: %v", err)
	}
	childWorktree := info.Worktrees[1]
	if got := gitOutput(t, git, childWorktree.Path, "rev-parse", "HEAD"); got != freshChild {
		t.Fatalf("child worktree HEAD = %s, want refreshed %s", got, freshChild)
	}
	if childWorktree.BaseSHA != freshChild {
		t.Fatalf("child BaseSHA = %s, want refreshed %s", childWorktree.BaseSHA, freshChild)
	}
}

func TestWorkspaceIntegrationCanonicalBaseRefAvoidsRemoteNameCollision(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, filepath.Join(tmp, "repo"))
	originURL := gitOutput(t, git, repo, "remote", "get-url", "origin")

	collisionUpdater := filepath.Join(tmp, "collision-updater")
	run(t, git, "clone", originURL, collisionUpdater)
	runGit(t, git, collisionUpdater, "config", "user.email", "ao@example.com")
	runGit(t, git, collisionUpdater, "config", "user.name", "Ao Agents")
	runGit(t, git, collisionUpdater, "checkout", "-b", "upstream/main", "origin/main")
	if err := os.WriteFile(filepath.Join(collisionUpdater, "collision.txt"), []byte("origin collision\n"), 0o644); err != nil {
		t.Fatalf("write collision file: %v", err)
	}
	runGit(t, git, collisionUpdater, "add", "collision.txt")
	runGit(t, git, collisionUpdater, "commit", "-m", "origin collision branch")
	collisionSHA := gitOutput(t, git, collisionUpdater, "rev-parse", "HEAD")
	runGit(t, git, collisionUpdater, "push", "origin", "HEAD:refs/heads/upstream/main")
	runGit(t, git, repo, "fetch", "origin", "+refs/heads/upstream/main:refs/remotes/origin/upstream/main")

	upstreamBare := filepath.Join(tmp, "upstream.git")
	run(t, git, "init", "--bare", upstreamBare)
	upstreamUpdater := filepath.Join(tmp, "upstream-updater")
	run(t, git, "clone", originURL, upstreamUpdater)
	runGit(t, git, upstreamUpdater, "config", "user.email", "ao@example.com")
	runGit(t, git, upstreamUpdater, "config", "user.name", "Ao Agents")
	runGit(t, git, upstreamUpdater, "checkout", "-B", "main", "origin/main")
	if err := os.WriteFile(filepath.Join(upstreamUpdater, "upstream.txt"), []byte("configured upstream\n"), 0o644); err != nil {
		t.Fatalf("write upstream file: %v", err)
	}
	runGit(t, git, upstreamUpdater, "add", "upstream.txt")
	runGit(t, git, upstreamUpdater, "commit", "-m", "configured upstream main")
	upstreamSHA := gitOutput(t, git, upstreamUpdater, "rev-parse", "HEAD")
	runGit(t, git, upstreamUpdater, "remote", "add", "upstream-target", upstreamBare)
	runGit(t, git, upstreamUpdater, "push", "upstream-target", "HEAD:main")
	runGit(t, git, repo, "remote", "add", "upstream", upstreamBare)

	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	target, err := ws.ResolveDefaultBranch(context.Background(), repo, "upstream/main")
	if err != nil {
		t.Fatalf("resolve qualified default: %v", err)
	}
	if got, want := target.BaseRef, "refs/remotes/upstream/main"; got != want {
		t.Fatalf("qualified base ref = %q, want %q", got, want)
	}
	if err := ws.FetchDefaultBranch(context.Background(), repo, target); err != nil {
		t.Fatalf("fetch qualified default: %v", err)
	}
	info, err := ws.Create(context.Background(), ports.WorkspaceConfig{
		ProjectID:  "proj",
		SessionID:  "sess-upstream",
		Branch:     "ao/proj-upstream",
		BaseBranch: "upstream/main",
		BaseRef:    target.BaseRef,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := gitOutput(t, git, info.Path, "rev-parse", "HEAD"); got != upstreamSHA {
		t.Fatalf("worktree HEAD = %s, want configured upstream %s (origin collision was %s)", got, upstreamSHA, collisionSHA)
	}
}

func requireGit(t *testing.T) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not found")
	}
	return git
}

func setupOriginClone(t *testing.T, git, tmp string) string {
	t.Helper()
	origin := filepath.Join(tmp, "origin.git")
	seed := filepath.Join(tmp, "seed")
	repo := filepath.Join(tmp, "repo")
	run(t, git, "init", "--bare", origin)
	run(t, git, "init", seed)
	runGit(t, git, seed, "config", "core.autocrlf", "false")
	runGit(t, git, seed, "config", "user.email", "ao@example.com")
	runGit(t, git, seed, "config", "user.name", "Ao Agents")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	runGit(t, git, seed, "add", "README.md")
	runGit(t, git, seed, "commit", "-m", "seed")
	runGit(t, git, seed, "branch", "-M", "main")
	runGit(t, git, seed, "remote", "add", "origin", origin)
	runGit(t, git, seed, "push", "-u", "origin", "main")
	runGit(t, git, origin, "symbolic-ref", "HEAD", "refs/heads/main")
	run(t, git, "clone", origin, repo)
	runGit(t, git, repo, "config", "core.autocrlf", "false")
	runGit(t, git, repo, "config", "user.email", "ao@example.com")
	runGit(t, git, repo, "config", "user.name", "Ao Agents")
	runGit(t, git, repo, "checkout", "main")
	runGit(t, git, repo, "reset", "--hard", "HEAD")
	return repo
}

func setupOriginCloneOnBranch(t *testing.T, git, tmp, branch string) string {
	t.Helper()
	repo := setupOriginClone(t, git, tmp)
	runGit(t, git, repo, "branch", "-m", "main", branch)
	runGit(t, git, repo, "push", "-u", "origin", branch)
	origin := gitOutput(t, git, repo, "remote", "get-url", "origin")
	runGit(t, git, origin, "symbolic-ref", "HEAD", "refs/heads/"+branch)
	runGit(t, git, repo, "remote", "set-head", "origin", branch)
	return repo
}

func pushRemoteBranchCommit(t *testing.T, git, repo, defaultBranch, branch string) (defaultSHA, branchSHA string) {
	t.Helper()
	defaultSHA = gitOutput(t, git, repo, "rev-parse", "refs/heads/"+defaultBranch)
	runGit(t, git, repo, "switch", "-c", branch)
	if err := os.WriteFile(filepath.Join(repo, "SESSION.md"), []byte("remote session work\n"), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}
	runGit(t, git, repo, "add", "SESSION.md")
	runGit(t, git, repo, "commit", "-m", "session work")
	runGit(t, git, repo, "push", "-u", "origin", branch)
	branchSHA = gitOutput(t, git, repo, "rev-parse", "HEAD")
	runGit(t, git, repo, "switch", defaultBranch)
	runGit(t, git, repo, "branch", "-D", branch)
	return defaultSHA, branchSHA
}

func gitOutput(t *testing.T, git, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(git, append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s -C %s %s: %v\n%s", git, dir, strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func runGit(t *testing.T, git, dir string, args ...string) {
	t.Helper()
	run(t, git, append([]string{"-C", dir}, args...)...)
}

func outputGit(t *testing.T, git, dir string, args ...string) []byte {
	t.Helper()
	out, err := exec.Command(git, append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s -C %s %s: %v\n%s", git, dir, strings.Join(args, " "), err, out)
	}
	return out
}

func run(t *testing.T, binary string, args ...string) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", binary, strings.Join(args, " "), err, out)
	}
}

func TestWorkspaceIntegrationOnlyStartRefMovesTheSeed(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, filepath.Join(tmp, "repo"))
	childRepo := setupOriginClone(t, git, filepath.Join(tmp, "child"))
	defaultSHA := gitOutput(t, git, repo, "rev-parse", "origin/main")
	runGit(t, git, repo, "checkout", "-b", "side")
	if err := os.WriteFile(filepath.Join(repo, "side.txt"), []byte("side\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, git, repo, "add", "side.txt")
	runGit(t, git, repo, "commit", "-m", "side commit")
	sideSHA := gitOutput(t, git, repo, "rev-parse", "HEAD")
	runGit(t, git, repo, "checkout", "main")
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	single := func(session, branch, startRef string) string {
		info, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: domain.SessionID(session), Branch: branch, FreshBranch: true, BaseRef: sideSHA, StartRef: startRef})
		if err != nil {
			t.Fatal(err)
		}
		return gitOutput(t, git, info.Path, "rev-parse", "HEAD")
	}
	if got := single("ordinary", "ao/ordinary", ""); got != defaultSHA {
		t.Fatalf("ordinary fresh branch starts at %s, want the default %s", got, defaultSHA)
	}
	if got := single("imported", "ao/imported", sideSHA); got != sideSHA {
		t.Fatalf("imported branch starts at %s, want %s", got, sideSHA)
	}
	project := func(session, startRef string) string {
		info, err := ws.CreateWorkspaceProject(ctx, ports.WorkspaceProjectConfig{
			ProjectID: "proj", SessionID: domain.SessionID(session), Kind: domain.KindWorker, Branch: "ao/" + session,
			RootRepoPath: repo, BaseRef: sideSHA, StartRef: startRef,
			Repos: []ports.WorkspaceProjectRepoConfig{{Name: "api", RelativePath: "api", RepoPath: childRepo}},
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ws.DestroyWorkspaceProject(ctx, info) })
		return gitOutput(t, git, info.Worktrees[0].Path, "rev-parse", "HEAD")
	}
	if got := project("ordinary-root", ""); got != defaultSHA {
		t.Fatalf("ordinary workspace root starts at %s, want the default %s", got, defaultSHA)
	}
	if got := project("imported-root", sideSHA); got != sideSHA {
		t.Fatalf("imported workspace root starts at %s, want %s", got, sideSHA)
	}
}

func TestWorkspaceIntegrationDestroyLiftsStaleAddLock(t *testing.T) {
	testDestroyLiftsStaleAddLock(t)
}

func TestWorkspaceIntegrationDestroyLiftsStaleAddLockUnderTranslatedLocale(t *testing.T) {
	t.Setenv("LC_ALL", "de_DE.UTF-8")
	t.Setenv("LANG", "de_DE.UTF-8")
	t.Setenv("LANGUAGE", "de")
	testDestroyLiftsStaleAddLock(t)
}

func testDestroyLiftsStaleAddLock(t *testing.T) {
	t.Helper()
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	info, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess-stale", Branch: "child-stale"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	runGit(t, git, repo, "worktree", "lock", "--reason", "initializing", info.Path)

	if err := ws.Destroy(ctx, info); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if _, err := os.Stat(info.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worktree still on disk: %v", err)
	}
}

func TestWorkspaceIntegrationDestroyStaleAddLockStillPreservesDirtyWork(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	info, err := ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess-dirty", Branch: "child-dirty"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := os.WriteFile(filepath.Join(info.Path, "agent-work.txt"), []byte("unsaved\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, git, repo, "worktree", "lock", "--reason", "initializing", info.Path)

	if err := ws.Destroy(ctx, info); !errors.Is(err, ports.ErrWorkspaceDirty) {
		t.Fatalf("destroy = %v, want ports.ErrWorkspaceDirty", err)
	}
	if _, err := os.Stat(filepath.Join(info.Path, "agent-work.txt")); err != nil {
		t.Fatalf("dirty work lost: %v", err)
	}
}
