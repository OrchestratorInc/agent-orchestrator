package gitworktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestWorkspaceIntegrationStashApplyRoundTrip(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	root := filepath.Join(tmp, "managed")
	ws, err := New(Options{Binary: git, ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	cfg := ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess-preserve", Branch: "feature/preserve"}

	info, err := ws.Create(ctx, cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := os.WriteFile(filepath.Join(info.Path, ".gitignore"), []byte("secret.txt\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	runGit(t, git, info.Path, "add", ".gitignore")
	runGit(t, git, info.Path, "commit", "-m", "add gitignore")

	if err := os.WriteFile(filepath.Join(info.Path, "README.md"), []byte("edited by agent\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if err := os.WriteFile(filepath.Join(info.Path, "agent-work.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write agent-work.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(info.Path, "secret.txt"), []byte("super-secret\n"), 0o644); err != nil {
		t.Fatalf("write secret.txt: %v", err)
	}

	ref, err := ws.StashUncommitted(ctx, info)
	if err != nil {
		t.Fatalf("StashUncommitted: %v", err)
	}
	if ref == "" {
		t.Fatal("StashUncommitted returned empty ref for dirty worktree")
	}
	if !strings.HasPrefix(ref, "refs/ao/preserved/") {
		t.Fatalf("ref = %q, want refs/ao/preserved/... prefix", ref)
	}

	if err := ws.ForceDestroy(ctx, info); err != nil {
		t.Fatalf("ForceDestroy: %v", err)
	}
	if _, err := os.Stat(info.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worktree path still exists after ForceDestroy")
	}

	restored, err := ws.Restore(ctx, cfg)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if restored.Path != info.Path {
		t.Fatalf("restored path = %q, want %q", restored.Path, info.Path)
	}

	if err := ws.ApplyPreserved(ctx, restored, ref); err != nil {
		t.Fatalf("ApplyPreserved: %v", err)
	}

	readmeBytes, err := os.ReadFile(filepath.Join(restored.Path, "README.md"))
	if err != nil {
		t.Fatalf("read README after apply: %v", err)
	}
	if string(readmeBytes) != "edited by agent\n" {
		t.Fatalf("README content = %q, want %q", string(readmeBytes), "edited by agent\n")
	}

	if _, err := os.Stat(filepath.Join(restored.Path, "agent-work.go")); err != nil {
		t.Fatalf("agent-work.go missing after apply: %v", err)
	}

	if _, err := os.Stat(filepath.Join(restored.Path, "secret.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("secret.txt exists after apply but must not (it was .gitignore-d)")
	}

	checkRefArgs := revParseVerifyArgs(repo, ref)
	if out, err := ws.run(ctx, ws.binary, checkRefArgs...); err == nil {
		t.Fatalf("preserve ref %q still exists after successful ApplyPreserved (points to %s)", ref, strings.TrimSpace(string(out)))
	}
}

func TestWorkspaceIntegrationApplyPreservedConflict(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	root := filepath.Join(tmp, "managed")
	ws, err := New(Options{Binary: git, ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	cfg := ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess-conflict", Branch: "feature/conflict-test"}

	info, err := ws.Create(ctx, cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	conflictFile := filepath.Join(info.Path, "shared.txt")
	if err := os.WriteFile(conflictFile, []byte("A\n"), 0o644); err != nil {
		t.Fatalf("write base A: %v", err)
	}
	runGit(t, git, info.Path, "add", "shared.txt")
	runGit(t, git, info.Path, "commit", "-m", "base: A")

	if err := os.WriteFile(conflictFile, []byte("B\n"), 0o644); err != nil {
		t.Fatalf("write B: %v", err)
	}

	ref, err := ws.StashUncommitted(ctx, info)
	if err != nil {
		t.Fatalf("StashUncommitted: %v", err)
	}
	if ref == "" {
		t.Fatal("StashUncommitted returned empty ref for dirty worktree")
	}

	if err := ws.ForceDestroy(ctx, info); err != nil {
		t.Fatalf("ForceDestroy: %v", err)
	}

	restored, err := ws.Restore(ctx, cfg)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}

	conflictFileRestored := filepath.Join(restored.Path, "shared.txt")
	if err := os.WriteFile(conflictFileRestored, []byte("C\n"), 0o644); err != nil {
		t.Fatalf("write C: %v", err)
	}
	runGit(t, git, restored.Path, "add", "shared.txt")

	applyErr := ws.ApplyPreserved(ctx, restored, ref)
	if applyErr == nil {
		t.Fatal("ApplyPreserved returned nil, want ErrPreservedConflict")
	}
	if !errors.Is(applyErr, ErrPreservedConflict) {
		t.Fatalf("ApplyPreserved error = %v, want errors.Is(..., ErrPreservedConflict)", applyErr)
	}

	checkRefArgs := revParseVerifyArgs(repo, ref)
	if _, err := ws.run(ctx, ws.binary, checkRefArgs...); err != nil {
		t.Fatalf("preserve ref %q was deleted after a conflicting apply, must be kept: %v", ref, err)
	}

	contents, err := os.ReadFile(conflictFileRestored)
	if err != nil {
		t.Fatalf("read conflicting file: %v", err)
	}
	if !strings.Contains(string(contents), "<<<<<<<") {
		t.Fatalf("conflicting file has no conflict markers after ApplyPreserved conflict; content:\n%s", string(contents))
	}
}

func TestWorkspaceIntegrationStashCleanWorktree(t *testing.T) {
	git := requireGit(t)
	tmp := t.TempDir()
	repo := setupOriginClone(t, git, tmp)
	root := filepath.Join(tmp, "managed")
	ws, err := New(Options{Binary: git, ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx := context.Background()
	cfg := ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess-clean", Branch: "feature/clean-stash"}

	info, err := ws.Create(ctx, cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	ref, err := ws.StashUncommitted(ctx, info)
	if err != nil {
		t.Fatalf("StashUncommitted on clean worktree: %v", err)
	}
	if ref != "" {
		t.Fatalf("StashUncommitted on clean worktree returned non-empty ref %q, want empty", ref)
	}

	if err := ws.Destroy(ctx, info); err != nil {
		t.Fatalf("destroy clean worktree: %v", err)
	}
}
