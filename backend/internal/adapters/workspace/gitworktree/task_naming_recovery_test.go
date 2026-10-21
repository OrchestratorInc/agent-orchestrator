package gitworktree

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type taskNamingCrash struct {
	Git, Managed, Repo, Stage, Marker string
	Info                              ports.WorkspaceProjectInfo
}

func TestTaskNamingCrashHelper(t *testing.T) {
	path := os.Getenv("AO_TASK_NAMING_CRASH_HELPER")
	if path == "" {
		return
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var crash taskNamingCrash
	if err := json.Unmarshal(body, &crash); err != nil {
		t.Fatal(err)
	}
	ws, err := New(Options{Binary: crash.Git, ManagedRoot: crash.Managed, RepoResolver: StaticRepoResolver{"proj": crash.Repo}})
	if err != nil {
		t.Fatal(err)
	}
	pause := func() {
		if err := os.WriteFile(crash.Marker, []byte("paused"), 0o600); err != nil {
			t.Fatal(err)
		}
		for {
			time.Sleep(time.Hour)
		}
	}
	run := ws.run
	ws.run = func(ctx context.Context, binary string, args ...string) ([]byte, error) {
		command := strings.Join(args, " ")
		if crash.Stage == "move" && strings.Contains(command, "worktree repair") {
			pause()
		}
		out, err := run(ctx, binary, args...)
		if err == nil && crash.Stage == "branch" && strings.Contains(command, "branch -m") {
			pause()
		}
		return out, err
	}
	if _, err := ws.NameTaskWorkspace(context.Background(), crash.Info, "ao/fix-login/root", "fix-login"); err != nil {
		t.Fatal(err)
	}
	pause() // Git is repaired, but the caller has not saved paths to SQLite.
}

func TestNameTaskWorkspaceRecoversHardKill(t *testing.T) {
	for _, multi := range []bool{false, true} {
		for _, stage := range []string{"branch", "move", "persist"} {
			t.Run(stage+map[bool]string{false: "/single", true: "/multi"}[multi], func(t *testing.T) {
				git := requireGit(t)
				tmp := t.TempDir()
				repo := setupOriginClone(t, git, filepath.Join(tmp, "root"))
				managed := filepath.Join(tmp, "managed")
				newWorkspace := func() *Workspace {
					ws, err := New(Options{Binary: git, ManagedRoot: managed, RepoResolver: StaticRepoResolver{"proj": repo}})
					if err != nil {
						t.Fatal(err)
					}
					return ws
				}
				ws := newWorkspace()
				var info ports.WorkspaceProjectInfo
				var err error
				if multi {
					child := setupOriginClone(t, git, filepath.Join(tmp, "child"))
					info, err = ws.CreateWorkspaceProject(t.Context(), ports.WorkspaceProjectConfig{ProjectID: "proj", SessionID: "proj-1", Kind: domain.KindWorker, Branch: "ao/proj-1", RootRepoPath: repo, BaseBranch: "main", Repos: []ports.WorkspaceProjectRepoConfig{{Name: "child", RepoPath: child, RelativePath: "packages/child", BaseBranch: "main"}}})
				} else {
					info.Root, err = ws.Create(t.Context(), ports.WorkspaceConfig{ProjectID: "proj", SessionID: "proj-1", Branch: "ao/proj-1/root", BaseBranch: "main"})
				}
				if err != nil {
					t.Fatal(err)
				}
				crash := taskNamingCrash{Git: git, Managed: managed, Repo: repo, Stage: stage, Marker: filepath.Join(tmp, "paused"), Info: info}
				body, err := json.Marshal(crash)
				if err != nil {
					t.Fatal(err)
				}
				input := filepath.Join(tmp, "crash.json")
				if err := os.WriteFile(input, body, 0o600); err != nil {
					t.Fatal(err)
				}
				output, err := os.Create(filepath.Join(tmp, "child.log"))
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = output.Close() }()
				cmd := exec.Command(os.Args[0], "-test.run=^TestTaskNamingCrashHelper$") //nolint:gosec // this package's own test binary
				cmd.Env = append(os.Environ(), "AO_TASK_NAMING_CRASH_HELPER="+input)
				cmd.Stdout, cmd.Stderr = output, output
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
				deadline := time.Now().Add(30 * time.Second)
				for {
					if _, err := os.Stat(crash.Marker); err == nil {
						break
					}
					if time.Now().After(deadline) {
						log, _ := os.ReadFile(output.Name())
						t.Fatalf("child did not reach crash point: %s", log)
					}
					time.Sleep(10 * time.Millisecond)
				}
				if err := cmd.Process.Kill(); err != nil {
					t.Fatal(err)
				}
				_ = cmd.Wait()
				// A fresh adapter has no in-memory state from the killed process.
				ws = newWorkspace()
				actual, found, err := ws.RecoverTaskWorkspace(t.Context(), "proj", "proj-1")
				if err != nil || !found || actual.Root.Branch != "ao/fix-login/root" || filepath.Base(actual.Root.Path) != "fix-login" {
					t.Fatalf("recovery = %+v, %v, %v", actual, found, err)
				}
				if got := gitOutput(t, git, actual.Root.Path, "branch", "--show-current"); got != actual.Root.Branch {
					t.Fatalf("root branch = %q", got)
				}
				for _, wt := range actual.Worktrees {
					if got := gitOutput(t, git, wt.Path, "branch", "--show-current"); got != actual.Root.Branch {
						t.Fatalf("child branch = %q", got)
					}
					if !strings.Contains(gitOutput(t, git, wt.RepoPath, "worktree", "list", "--porcelain"), filepath.ToSlash(wt.Path)) {
						t.Fatal("Git registration was not repaired")
					}
				}
				if err := ws.CompleteTaskWorkspaceNaming(t.Context(), actual); err != nil {
					t.Fatal(err)
				}
				if _, found, err := ws.RecoverTaskWorkspace(t.Context(), "proj", "proj-1"); found || err != nil {
					t.Fatalf("acknowledged move remains pending: %v, %v", found, err)
				}
			})
		}
	}
}

func TestNameTaskWorkspaceMoveRefusesOccupiedDestination(t *testing.T) {
	for _, kind := range []string{"file", "empty-directory", "directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			tmp := t.TempDir()
			source, target := filepath.Join(tmp, "source"), filepath.Join(tmp, "target")
			if err := os.Mkdir(source, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(source, "task"), []byte("task"), 0o600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "file":
				if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "empty-directory", "directory":
				if err := os.Mkdir(target, 0o700); err != nil {
					t.Fatal(err)
				}
				if kind == "directory" {
					if err := os.WriteFile(filepath.Join(target, "keep"), []byte("keep"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			case "symlink":
				if err := os.Symlink(source, target); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}
			before, err := os.Lstat(target)
			if err != nil {
				t.Fatal(err)
			}
			if err := moveTaskDirectory(source, target); err == nil {
				t.Fatal("overwrote occupied destination")
			}
			after, err := os.Lstat(target)
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("destination replaced: %v", err)
			}
			if body, err := os.ReadFile(filepath.Join(source, "task")); err != nil || string(body) != "task" {
				t.Fatalf("source changed: %s, %v", body, err)
			}
		})
	}
}

func TestTaskNamingRecoveryRefusesOccupiedDestination(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, filepath.Join(tmp, "root"))
	ws, err := New(Options{Binary: git, ManagedRoot: filepath.Join(tmp, "managed"), RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatal(err)
	}
	root, err := ws.Create(t.Context(), ports.WorkspaceConfig{ProjectID: "proj", SessionID: "proj-1", Branch: "ao/proj-1/root", BaseBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	original := ports.WorkspaceProjectInfo{Root: root}
	original.Root.RepoPath = repo
	target := original
	target.Root.Path = filepath.Join(filepath.Dir(root.Path), "login-redirect")
	target.Root.Branch = "ao/login-redirect/root"
	gitDir, err := checkoutGitDir(root.Path)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := ws.taskNamingJournal("proj", "proj-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := writeTaskNamingMove(t.Context(), journal, taskNamingMove{Original: original, Target: target, GitDirs: []string{gitDir}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target.Root.Path, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, found, err := ws.RecoverTaskWorkspace(t.Context(), "proj", "proj-1"); !found || err == nil {
		t.Fatalf("accepted occupied destination: %v, %v", found, err)
	}
	if body, err := os.ReadFile(target.Root.Path); err != nil || string(body) != "keep" {
		t.Fatalf("destination changed: %s, %v", body, err)
	}
	if got := gitOutput(t, git, root.Path, "branch", "--show-current"); got != root.Branch {
		t.Fatalf("source branch changed: %s", got)
	}
}
