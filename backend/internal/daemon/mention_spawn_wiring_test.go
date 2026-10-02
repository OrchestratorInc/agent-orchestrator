package daemon

import (
	"context"
	"io"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMentionSpawnEnabled(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cases := map[string]bool{"": false, "0": false, "false": false, "nope": false, "1": true, "true": true, "TRUE": true}
	for raw, want := range cases {
		t.Setenv(mentionSpawnEnv, raw)
		if got := mentionSpawnEnabled(logger); got != want {
			t.Fatalf("%s=%q: enabled = %v, want %v", mentionSpawnEnv, raw, got, want)
		}
	}
}

func TestSafeBranchName(t *testing.T) {
	for _, ok := range []string{"feature/login", "fix-123", "ao/vibeli-406/root", "release_2026.10"} {
		if !safeBranchName(ok) {
			t.Fatalf("%q rejected", ok)
		}
	}
	for _, bad := range []string{"", "-upload-pack=x", "a..b", "a b", "a:b", "a~1", "a^", "a?", "a*", "a[", `a\b`} {
		if safeBranchName(bad) {
			t.Fatalf("%q accepted", bad)
		}
	}
}

// TestGitBranchFetcherMakesPRBranchResolvable is the reason the fetcher exists:
// a branch pushed by a person after the project was cloned must resolve as
// origin/<branch> before the worktree is created, or the worktree silently
// starts from the default branch.
func TestGitBranchFetcherMakesPRBranchResolvable(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	remote := filepath.Join(dir, "remote")
	clone := filepath.Join(dir, "clone")
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Env = append(cmd.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main", remote)
	git("-C", remote, "commit", "-q", "--allow-empty", "-m", "base")
	git("clone", "-q", remote, clone)
	// A human pushes a PR branch after AO cloned the project.
	git("-C", remote, "checkout", "-q", "-b", "feature/login")
	git("-C", remote, "commit", "-q", "--allow-empty", "-m", "work")
	want := git("-C", remote, "rev-parse", "HEAD")

	if err := (gitBranchFetcher{}).FetchBranch(context.Background(), clone, "feature/login"); err != nil {
		t.Fatal(err)
	}
	if got := git("-C", clone, "rev-parse", "origin/feature/login"); got != want {
		t.Fatalf("origin/feature/login = %s, want %s", got, want)
	}
	if err := (gitBranchFetcher{}).FetchBranch(context.Background(), clone, "no-such-branch"); err == nil {
		t.Fatal("fetching a missing branch succeeded")
	}
}
