package sessionmove

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestMoveToCloudAndBack(t *testing.T) {
	f := newFixture(t)
	exclude := makeDirty(t, f)
	want := expectedTarget(t, f)
	before := stateOf(t, f.src)

	c, err := CaptureWorktree(t.Context(), CaptureOptions{Worktree: f.src, Label: "MacBook A", Exclude: exclude})
	if err != nil {
		t.Fatal(err)
	}
	if after := stateOf(t, f.src); after != before {
		t.Fatalf("capture changed the source worktree:\nbefore %+v\nafter  %+v", before, after)
	}
	if c.Branch != "feat/x" || c.Base != f.c2 || c.Commit == "" {
		t.Fatalf("capture = %+v", c)
	}
	if got := g(t, f.src, "log", "-1", "--format=%s", c.Commit); got != "WIP: uncommitted changes from MacBook A" {
		t.Fatalf("message = %q", got)
	}
	status := map[string]byte{}
	for _, ch := range c.Changes {
		status[ch.Path] = ch.Status
		if ch.Path == "assets/logo.bin" && ch.Size != 64<<10 {
			t.Fatalf("logo.bin size = %d", ch.Size)
		}
	}
	wantStatus := map[string]byte{
		"README.md": 'M', "src/b.go": 'M', "src/c.go": 'M', "docs/old.md": 'D', "src/a.go": 'D',
		"src/a2.go": 'A', "notes/new.txt": 'A', "assets/logo.bin": 'A', "link.txt": 'A',
	}
	if len(status) != len(wantStatus) {
		t.Fatalf("changes = %v, want %v", status, wantStatus)
	}
	for p, s := range wantStatus {
		if status[p] != s {
			t.Fatalf("%s status = %q, want %q (all: %v)", p, status[p], s, status)
		}
	}

	if err := PushBranch(t.Context(), f.src, c, GitHubFileLimit); err != nil {
		t.Fatal(err)
	}
	if got := g(t, f.origin, "rev-parse", "refs/heads/feat/x"); got != c.Commit {
		t.Fatalf("origin feat/x = %s, want %s", got, c.Commit)
	}
	// The source now matches origin: the branch is at the WIP commit and only
	// the excluded symlink is left over.
	if got := g(t, f.src, "rev-parse", "HEAD"); got != c.Commit {
		t.Fatalf("source HEAD = %s, want %s", got, c.Commit)
	}
	if got := g(t, f.src, "status", "--porcelain"); got != "?? config.local" {
		t.Fatalf("source status after push:\n%s", got)
	}
	if got := g(t, f.src, "ls-files", "-t", "config.yml"); got != "S config.yml" {
		t.Fatalf("skip-worktree lost: %q", got)
	}
	if data, _ := os.ReadFile(filepath.Join(f.src, "config.yml")); string(data) != "env: local-dev\n" {
		t.Fatalf("local config.yml changed: %q", data)
	}

	// What the Cloud worker does: a shallow clone of every branch, then
	// checkout --no-track -B <branch> origin/<branch>.
	cloud := filepath.Join(f.tmp, "cloud")
	g(t, f.tmp, "clone", "-q", "--depth=1", "--no-single-branch", "file://"+f.origin, cloud)
	g(t, cloud, "checkout", "-q", "--no-track", "-B", "feat/x", "origin/feat/x")
	assertSameFiles(t, files(t, cloud), want)

	// Back on a machine whose clone only has main: Cloud pushed the branch,
	// and the import fetches and places it.
	dst := filepath.Join(f.tmp, "dst")
	g(t, f.tmp, "clone", "-q", "--single-branch", "--branch", "main", "file://"+f.origin, dst)
	if err := ImportBranch(t.Context(), dst, "feat/x", c.Commit); err != nil {
		t.Fatal(err)
	}
	checkout := filepath.Join(f.tmp, "dst-session")
	g(t, dst, "worktree", "add", "-q", checkout, "feat/x")
	assertSameFiles(t, files(t, checkout), want)
}

func TestCleanWorktreePushesCommitsOnly(t *testing.T) {
	f := newFixture(t)
	c, err := CaptureWorktree(t.Context(), CaptureOptions{Worktree: f.src})
	if err != nil {
		t.Fatal(err)
	}
	if c.Commit != "" || c.Tip() != f.c2 || len(c.Changes) != 0 {
		t.Fatalf("clean capture = %+v", c)
	}
	if err := PushBranch(t.Context(), f.src, c, GitHubFileLimit); err != nil {
		t.Fatal(err)
	}
	if got := g(t, f.origin, "rev-parse", "refs/heads/feat/x"); got != f.c2 {
		t.Fatalf("origin feat/x = %s, want c2 %s", got, f.c2)
	}
	if got := g(t, f.src, "rev-parse", "HEAD"); got != f.c2 {
		t.Fatalf("a clean push moved the source to %s", got)
	}
}

func TestImportBranchPlacesSafely(t *testing.T) {
	f := newFixture(t)
	writeFile(t, filepath.Join(f.src, "README.md"), "edited\n", 0o644)
	c, err := CaptureWorktree(t.Context(), CaptureOptions{Worktree: f.src})
	if err != nil {
		t.Fatal(err)
	}
	if err := PushBranch(t.Context(), f.src, c, GitHubFileLimit); err != nil {
		t.Fatal(err)
	}
	clone := func(name string) string {
		dst := filepath.Join(f.tmp, name)
		g(t, f.tmp, "clone", "-q", "file://"+f.origin, dst)
		return dst
	}

	t.Run("fast-forwards an older copy", func(t *testing.T) {
		dst := clone("ff")
		g(t, dst, "branch", "feat/x", f.c1)
		if err := ImportBranch(t.Context(), dst, "feat/x", c.Commit); err != nil {
			t.Fatal(err)
		}
		if got := g(t, dst, "rev-parse", "feat/x"); got != c.Commit {
			t.Fatalf("feat/x = %s, want %s", got, c.Commit)
		}
	})
	t.Run("refuses a diverged branch", func(t *testing.T) {
		dst := clone("diverged")
		g(t, dst, "switch", "-q", "-c", "feat/x", f.c1)
		writeFile(t, filepath.Join(dst, "README.md"), "theirs\n", 0o644)
		g(t, dst, "commit", "-q", "-am", "theirs")
		theirs := g(t, dst, "rev-parse", "HEAD")
		g(t, dst, "switch", "-q", "main")
		if err := ImportBranch(t.Context(), dst, "feat/x", c.Commit); !errors.Is(err, ErrDiverged) {
			t.Fatalf("err = %v, want ErrDiverged", err)
		}
		if got := g(t, dst, "rev-parse", "feat/x"); got != theirs {
			t.Fatal("refused import still moved the branch")
		}
	})
	t.Run("refuses a checked-out branch", func(t *testing.T) {
		dst := clone("checked-out")
		g(t, dst, "switch", "-q", "-c", "feat/x", f.c1)
		if err := ImportBranch(t.Context(), dst, "feat/x", c.Commit); !errors.Is(err, ErrBranchCheckedOut) {
			t.Fatalf("err = %v, want ErrBranchCheckedOut", err)
		}
	})
	t.Run("refuses a commit origin does not have", func(t *testing.T) {
		dst := clone("missing")
		writeFile(t, filepath.Join(f.src, "local.txt"), "never pushed\n", 0o644)
		g(t, f.src, "add", "local.txt")
		g(t, f.src, "commit", "-q", "-m", "local only")
		if err := ImportBranch(t.Context(), dst, "feat/x", g(t, f.src, "rev-parse", "HEAD")); !errors.Is(err, ErrMissingCommit) {
			t.Fatalf("err = %v, want ErrMissingCommit", err)
		}
	})
}
