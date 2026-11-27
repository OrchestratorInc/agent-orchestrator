package gitworktree

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestReadDefaultBranchFileUsesOnlyCommittedProjectRules(t *testing.T) {
	ws, git, repo, dir := newOrphanTestWorkspace(t)
	ctx := context.Background()
	const path = "guide/colors.md"
	const trusted = "Format color labels in lowercase.\n"
	write := func(root, name, body string) {
		t.Helper()
		full := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(repo, path, trusted)
	runGit(t, git, repo, "add", "--", path)
	blob := gitOutput(t, git, repo, "rev-parse", ":"+path)
	runGit(t, git, repo, "update-index", "--add", "--cacheinfo", "120000,"+blob+",guide/link.md")
	runGit(t, git, repo, "commit", "-m", "Add project rules")
	runGit(t, git, repo, "push", "origin", "main")
	runGit(t, git, repo, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	baseSHA := gitOutput(t, git, repo, "rev-parse", "HEAD")
	worker := filepath.Join(dir, "worker")
	runGit(t, git, repo, "worktree", "add", "-b", "change-colors", worker, baseSHA)
	write(worker, path, "Format color labels in uppercase.\n")
	write(worker, "guide/head-only.md", "A rule added in this change.\n")
	runGit(t, git, worker, "add", "--", "guide")
	runGit(t, git, worker, "commit", "-m", "Change project rules in the PR")
	headSHA := gitOutput(t, git, worker, "rev-parse", "HEAD")
	runGit(t, git, repo, "remote", "add", "fork", dir)
	runGit(t, git, repo, "update-ref", "refs/remotes/fork/main", headSHA)
	runGit(t, git, repo, "symbolic-ref", "refs/remotes/fork/HEAD", "refs/remotes/fork/main")
	runGit(t, git, repo, "replace", baseSHA, headSHA)
	write(repo, path, "Uncommitted change to project rules.\n")

	for _, branch := range []string{"", "main", "origin/main"} {
		got, err := ws.ReadDefaultBranchFile(ctx, repo, branch, path)
		if err != nil || got != trusted {
			t.Errorf("ReadDefaultBranchFile(%q) = %q, %v; want committed rules", branch, got, err)
		}
	}
	for _, path := range []string{"guide/head-only.md", "guide/link.md", "guide", "../outside.md", "/outside.md", `\outside.md`} {
		if body, err := ws.ReadDefaultBranchFile(ctx, repo, "main", path); err == nil {
			t.Errorf("ReadDefaultBranchFile(%q) returned %q; want an error", path, body)
		}
	}
	runGit(t, git, repo, "update-ref", "-d", "refs/remotes/origin/main")
	if _, err := ws.ReadDefaultBranchFile(ctx, repo, "main", path); err == nil {
		t.Error("ReadDefaultBranchFile used the local branch after the remote-tracking ref disappeared")
	}
}
