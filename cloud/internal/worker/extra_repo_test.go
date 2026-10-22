package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func seedExtraRepoSource(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	gitRun(t, src, "init", "--initial-branch=main")
	gitRun(t, src, "config", "user.name", "AO Test")
	gitRun(t, src, "config", "user.email", "ao@example.test")
	if err := os.WriteFile(filepath.Join(src, "README.md"), []byte("hi\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, src, "add", "README.md")
	gitRun(t, src, "commit", "-m", "init")
	return src
}

// prepareExtra prepares an AO-owned additional checkout (not the user's
// workspace) and drops the adopted flag.
func prepareExtra(ctx context.Context, dest string, repo RepoRef, branch string) error {
	_, err := PrepareExtraRepo(ctx, ExecGitRunner{}, dest, repo, ExtraRepoOptions{SessionID: "abc123", SessionBranch: branch, DataDir: os.TempDir()})
	return err
}

func TestPrepareExtraRepoChecksOutTheSessionBranchAndReviewBase(t *testing.T) {
	src := seedExtraRepoSource(t)
	dest := filepath.Join(t.TempDir(), "extra")
	repo := RepoRef{URL: "file://" + src}
	if err := prepareExtra(context.Background(), dest, repo, "ao/abc123"); err != nil {
		t.Fatalf("PrepareExtraRepo: %v", err)
	}
	if branch := gitOutput(t, dest, "rev-parse", "--abbrev-ref", "HEAD"); branch != "ao/abc123" {
		t.Fatalf("checked out %q, want the session branch", branch)
	}
	if base := gitOutput(t, dest, "rev-parse", WorkspaceReviewBaseRef); base != gitOutput(t, src, "rev-parse", "HEAD") {
		t.Fatalf("review base %q is not the cloned commit", base)
	}
}

// A worker restart finds the checkout already there: it fetches instead of
// failing to clone, and keeps the agent's commits on the session branch.
func TestPrepareExtraRepoReusesAnExistingCheckout(t *testing.T) {
	src := seedExtraRepoSource(t)
	dest := filepath.Join(t.TempDir(), "extra")
	repo := RepoRef{URL: "file://" + src}
	ctx := context.Background()
	if err := prepareExtra(ctx, dest, repo, "ao/abc123"); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dest, "config", "user.name", "AO Test")
	gitRun(t, dest, "config", "user.email", "ao@example.test")
	if err := os.WriteFile(filepath.Join(dest, "work.txt"), []byte("agent work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dest, "add", "work.txt")
	gitRun(t, dest, "commit", "-m", "agent work")
	agentCommit := gitOutput(t, dest, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(src, "upstream.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, src, "add", "upstream.txt")
	gitRun(t, src, "commit", "-m", "upstream")

	if err := prepareExtra(ctx, dest, repo, "ao/abc123"); err != nil {
		t.Fatalf("second PrepareExtraRepo: %v", err)
	}
	if head := gitOutput(t, dest, "rev-parse", "HEAD"); head != agentCommit {
		t.Fatalf("restart moved the session branch to %s, want the agent's commit %s", head, agentCommit)
	}
	if fetched := gitOutput(t, dest, "rev-parse", "origin/main"); fetched != gitOutput(t, src, "rev-parse", "HEAD") {
		t.Fatalf("restart did not fetch: origin/main = %s", fetched)
	}
}

func TestPrepareExtraRepoLeavesOtherFoldersAlone(t *testing.T) {
	ctx := context.Background()
	t.Run("plain folder", func(t *testing.T) {
		dest := filepath.Join(t.TempDir(), "extra")
		if err := os.MkdirAll(dest, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dest, "notes.txt"), []byte("mine"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := prepareExtra(ctx, dest, RepoRef{URL: "file://" + seedExtraRepoSource(t)}, "ao/x")
		if err == nil || !strings.Contains(err.Error(), "not a Git checkout") {
			t.Fatalf("err = %v, want a not-a-checkout refusal", err)
		}
		if _, statErr := os.Stat(filepath.Join(dest, "notes.txt")); statErr != nil {
			t.Fatalf("the folder's file was touched: %v", statErr)
		}
	})
	t.Run("other repository", func(t *testing.T) {
		dest := filepath.Join(t.TempDir(), "extra")
		if err := prepareExtra(ctx, dest, RepoRef{URL: "file://" + seedExtraRepoSource(t)}, "ao/x"); err != nil {
			t.Fatal(err)
		}
		err := prepareExtra(ctx, dest, RepoRef{URL: "file://" + seedExtraRepoSource(t)}, "ao/x")
		if err == nil || !strings.Contains(err.Error(), "different repository") {
			t.Fatalf("err = %v, want a different-repository refusal", err)
		}
	})
}

func TestSessionRepoCheckoutsAndSelectors(t *testing.T) {
	workspace := "/home/dev/work/alice"
	checkouts := SessionRepoCheckouts(workspace, "https://github.com/acme/alice", []RepoRef{
		{URL: "https://github.com/acme/voice-calling-ai"}, {URL: ""}, {URL: "https://github.com/Acme/Julian.git", Branch: "dev"},
	})
	if len(checkouts) != 3 || !checkouts[0].Main || checkouts[0].FullName != "acme/alice" {
		t.Fatalf("checkouts = %+v", checkouts)
	}
	if checkouts[2].Path != "/home/dev/work/Julian" || checkouts[2].FullName != "acme/julian" || checkouts[2].Branch != "dev" {
		t.Fatalf("Julian checkout = %+v", checkouts[2])
	}
	for selector, want := range map[string]string{
		"": "alice", "acme/voice-calling-ai": "voice-calling-ai", "ACME/julian": "Julian",
		"julian": "Julian", "https://github.com/acme/alice.git": "alice",
	} {
		got, err := ResolveRepoCheckout(checkouts, selector)
		if err != nil || got.Name != want {
			t.Errorf("ResolveRepoCheckout(%q) = %+v, %v; want %s", selector, got, err, want)
		}
	}
	if _, err := ResolveRepoCheckout(checkouts, "acme/other"); err != ErrUnknownRepository {
		t.Fatalf("unknown selector err = %v", err)
	}
	standalone := SessionRepoCheckouts("/workspace/repository", "https://scratch.ao.local/p1", []RepoRef{{URL: "https://github.com/acme/x"}})
	if len(standalone) != 1 {
		t.Fatalf("standalone checkouts = %+v, want only the workspace", standalone)
	}
}

func TestPrepareStandaloneWorkspace(t *testing.T) {
	ctx := context.Background()
	filled := t.TempDir()
	if err := os.MkdirAll(filepath.Join(filled, "alice", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := PrepareStandaloneWorkspace(ctx, ExecGitRunner{}, filled, true); err != nil {
		t.Fatalf("adopting a filled folder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filled, ".git")); !os.IsNotExist(err) {
		t.Fatal("an adopted folder was git-initialized")
	}
	if err := PrepareStandaloneWorkspace(ctx, ExecGitRunner{}, filled, false); err == nil {
		t.Fatal("a filled non-repository folder must still be refused without adoption")
	}
	empty := filepath.Join(t.TempDir(), "standalone")
	if err := PrepareStandaloneWorkspace(ctx, ExecGitRunner{}, empty, true); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(empty, ".git")); err != nil || !info.IsDir() {
		t.Fatalf("an empty standalone folder was not initialized: %v", err)
	}
}

// In the user's own workspace an existing checkout of the repository is
// adopted: AO writes nothing into its git configuration, stashes the user's
// uncommitted changes and starts the session branch from origin's branch, and
// a restart keeps the agent's commits.
func TestPrepareExtraRepoAdoptsTheUsersCheckoutWithoutRewritingIt(t *testing.T) {
	ctx := context.Background()
	src := seedExtraRepoSource(t)
	dest := filepath.Join(t.TempDir(), "voice-calling-ai")
	gitRun(t, filepath.Dir(dest), "clone", "--quiet", "file://"+src, dest)
	gitRun(t, dest, "config", "user.name", "Dev")
	gitRun(t, dest, "config", "user.email", "dev@example.test")
	gitRun(t, dest, "checkout", "--quiet", "-b", "dev-feature")
	if err := os.WriteFile(filepath.Join(dest, "README.md"), []byte("local edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := gitOutput(t, dest, "config", "--local", "--list")
	repo := RepoRef{URL: "file://" + src}
	opts := ExtraRepoOptions{SessionID: "abc123", SessionBranch: "ao/abc123", DataDir: t.TempDir(), Adopt: true}
	adopted, err := PrepareExtraRepo(ctx, ExecGitRunner{}, dest, repo, opts)
	if err != nil || !adopted {
		t.Fatalf("PrepareExtraRepo = %v, %v; want an adopted checkout", adopted, err)
	}
	if after := gitOutput(t, dest, "config", "--local", "--list"); after != before {
		t.Fatalf("adoption rewrote the checkout's git config:\nbefore %s\nafter  %s", before, after)
	}
	if branch := gitOutput(t, dest, "rev-parse", "--abbrev-ref", "HEAD"); branch != "ao/abc123" {
		t.Fatalf("on %q, want the session branch", branch)
	}
	if head, base := gitOutput(t, dest, "rev-parse", "HEAD"), gitOutput(t, src, "rev-parse", "HEAD"); head != base {
		t.Fatalf("session branch starts at %s, want origin's main %s", head, base)
	}
	if stash := gitOutput(t, dest, "stash", "list"); !strings.Contains(stash, "ao-session abc123") {
		t.Fatalf("the user's edit was not stashed: %q", stash)
	}
	if mine := gitOutput(t, dest, "rev-parse", "--verify", "refs/heads/dev-feature"); mine == "" {
		t.Fatal("the user's branch is gone")
	}
	gitRun(t, dest, "-c", "user.name=AO", "-c", "user.email=ao@example.test", "commit", "--allow-empty", "-m", "agent")
	agent := gitOutput(t, dest, "rev-parse", "HEAD")
	if _, err := PrepareExtraRepo(ctx, ExecGitRunner{}, dest, repo, opts); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if head := gitOutput(t, dest, "rev-parse", "HEAD"); head != agent {
		t.Fatalf("restart moved the session branch to %s, want %s", head, agent)
	}
}

// A restored sandbox clones fresh; a session branch the agent already pushed
// is checked out from origin, so the next push fast-forwards.
func TestPrepareExtraRepoResumesAPushedSessionBranch(t *testing.T) {
	ctx := context.Background()
	src := seedExtraRepoSource(t)
	gitRun(t, src, "checkout", "--quiet", "-b", "ao/abc123")
	if err := os.WriteFile(filepath.Join(src, "pushed.txt"), []byte("pushed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, src, "add", "pushed.txt")
	gitRun(t, src, "commit", "-m", "pushed work")
	pushed := gitOutput(t, src, "rev-parse", "HEAD")
	gitRun(t, src, "checkout", "--quiet", "main")
	dest := filepath.Join(t.TempDir(), "extra")
	if err := prepareExtra(ctx, dest, RepoRef{URL: "file://" + src}, "ao/abc123"); err != nil {
		t.Fatal(err)
	}
	if head := gitOutput(t, dest, "rev-parse", "HEAD"); head != pushed {
		t.Fatalf("session branch at %s, want the pushed %s", head, pushed)
	}
}

func TestSessionRepoCheckoutsSkipsDotFoldersAndCollisions(t *testing.T) {
	workspace := "/home/dev/work/app"
	extras := []RepoRef{
		{URL: "https://github.com/acme/.aws"},
		{URL: "https://github.com/other/app"},
		{URL: "https://github.com/acme/web"},
		{URL: "https://github.com/fork/web"},
	}
	checkouts := SessionRepoCheckouts(workspace, "https://github.com/acme/web", extras)
	if len(checkouts) != 2 || checkouts[1].FullName != "acme/web" || checkouts[1].Path != "/home/dev/work/web" {
		t.Fatalf("checkouts = %+v, want the main checkout and acme/web only", checkouts)
	}
	if _, err := ResolveRepoCheckout(checkouts, "other/app"); err != ErrUnknownRepository {
		t.Fatalf("a repository colliding with the main checkout resolved: %v", err)
	}
	skipped := SkippedExtraRepos(workspace, "https://github.com/acme/web", extras)
	if len(skipped) != 3 {
		t.Fatalf("skipped = %+v, want .aws, other/app and fork/web", skipped)
	}
}

// A session branch named like an additional repository's base branch (the
// configured branch, or origin's default) is never checked out or pushed
// there: the repository stays at its base and the caller is told why.
func TestPrepareExtraRepoRefusesTheBaseBranchAsSessionBranch(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name, configured, branch string
	}{
		{"configured base", "develop", "develop"},
		{"origin default", "", "main"},
		{"case-insensitive", "", "MAIN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := seedExtraRepoSource(t)
			if tc.configured != "" {
				gitRun(t, src, "branch", tc.configured)
			}
			dest := filepath.Join(t.TempDir(), "extra")
			err := prepareExtra(ctx, dest, RepoRef{URL: "file://" + src, Branch: tc.configured}, tc.branch)
			if !errors.Is(err, ErrSessionBranchIsBase) {
				t.Fatalf("PrepareExtraRepo = %v, want ErrSessionBranchIsBase", err)
			}
			want := tc.configured
			if want == "" {
				want = "main"
			}
			if branch := gitOutput(t, dest, "rev-parse", "--abbrev-ref", "HEAD"); branch != want {
				t.Fatalf("checked out %q, want the base %q", branch, want)
			}
			if !SessionBranchIsBase(ctx, ExecGitRunner{}, dest, tc.configured, tc.branch) {
				t.Fatal("SessionBranchIsBase did not recognise the base branch")
			}
		})
	}
}
