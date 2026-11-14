package sessionmove

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCaptureRefusesUnsafeStates(t *testing.T) {
	// conflict makes feat/x and branch other both change README.md.
	conflict := func(t *testing.T, f fixture) {
		g(t, f.src, "switch", "-q", "-c", "other", "main")
		writeFile(t, filepath.Join(f.src, "README.md"), "other\n", 0o644)
		g(t, f.src, "commit", "-q", "-am", "other")
		g(t, f.src, "switch", "-q", "feat/x")
		writeFile(t, filepath.Join(f.src, "README.md"), "mine\n", 0o644)
		g(t, f.src, "commit", "-q", "-am", "mine")
	}
	cases := []struct {
		name  string
		setup func(t *testing.T, f fixture)
		want  error
	}{
		{"merge in progress", func(t *testing.T, f fixture) {
			conflict(t, f)
			_, _ = gitTry(f.src, "merge", "-q", "other")
		}, ErrOperationInProgress},
		{"rebase in progress", func(t *testing.T, f fixture) {
			conflict(t, f)
			_, _ = gitTry(f.src, "rebase", "-q", "other")
		}, ErrOperationInProgress},
		{"conflict left by stash pop", func(t *testing.T, f fixture) {
			writeFile(t, filepath.Join(f.src, "README.md"), "stashed\n", 0o644)
			g(t, f.src, "stash", "-q")
			writeFile(t, filepath.Join(f.src, "README.md"), "committed\n", 0o644)
			g(t, f.src, "commit", "-q", "-am", "committed")
			_, _ = gitTry(f.src, "stash", "pop", "-q")
			if _, err := gitTry(f.src, "rev-parse", "-q", "--verify", "MERGE_HEAD"); err == nil {
				t.Fatal("stash pop should not leave MERGE_HEAD")
			}
		}, ErrOperationInProgress},
		{"detached HEAD", func(t *testing.T, f fixture) {
			g(t, f.src, "switch", "-q", "--detach")
		}, ErrDetachedHead},
		{"no commits", func(t *testing.T, f fixture) {
			if err := os.RemoveAll(f.src); err != nil {
				t.Fatal(err)
			}
			g(t, f.tmp, "init", "-q", f.src)
			writeFile(t, filepath.Join(f.src, "x"), "x\n", 0o644)
		}, ErrNoCommits},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.setup(t, f)
			_, err := CaptureWorktree(t.Context(), CaptureOptions{Worktree: f.src})
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestCaptureSubmodules(t *testing.T) {
	f := newFixture(t)
	sub := filepath.Join(f.tmp, "sub.git")
	g(t, f.tmp, "init", "-q", "--bare", sub)
	seed := filepath.Join(f.tmp, "sub-seed")
	g(t, f.tmp, "init", "-q", seed)
	writeFile(t, filepath.Join(seed, "lib.txt"), "v1\n", 0o644)
	g(t, seed, "add", "-A")
	g(t, seed, "commit", "-q", "-m", "v1")
	g(t, seed, "push", "-q", sub, "main")
	g(t, f.src, "submodule", "add", "-q", sub, "vendor/lib")
	g(t, f.src, "commit", "-q", "-m", "add submodule")
	lib := filepath.Join(f.src, "vendor/lib")

	writeFile(t, filepath.Join(lib, "lib.txt"), "edited\n", 0o644)
	if _, err := CaptureWorktree(t.Context(), CaptureOptions{Worktree: f.src}); !errors.Is(err, ErrDirtySubmodule) {
		t.Fatalf("edited file in submodule: err = %v", err)
	}
	g(t, lib, "checkout", "-q", "lib.txt")
	writeFile(t, filepath.Join(lib, "untracked.txt"), "new\n", 0o644)
	if _, err := CaptureWorktree(t.Context(), CaptureOptions{Worktree: f.src}); !errors.Is(err, ErrDirtySubmodule) {
		t.Fatalf("untracked file in submodule: err = %v", err)
	}
	g(t, lib, "add", "untracked.txt")
	g(t, lib, "commit", "-q", "-m", "v2")
	c, err := CaptureWorktree(t.Context(), CaptureOptions{Worktree: f.src})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Submodules) != 1 || c.Submodules[0] != "vendor/lib" {
		t.Fatalf("moved submodules = %v", c.Submodules)
	}
	if len(c.Changes) != 1 || c.Changes[0].Path != "vendor/lib" || c.Changes[0].Size != 0 {
		t.Fatalf("changes = %+v", c.Changes)
	}
}

// A copy of an empty index would capture the files a sparse checkout leaves
// off disk as deletions. Starting from the real index keeps them.
func TestCaptureSparseCheckout(t *testing.T) {
	f := newFixture(t)
	g(t, f.src, "sparse-checkout", "set", "--cone", "src")
	if _, err := os.Stat(filepath.Join(f.src, "docs/old.md")); !os.IsNotExist(err) {
		t.Fatalf("docs/old.md should be outside the sparse checkout: %v", err)
	}
	writeFile(t, filepath.Join(f.src, "src/b.go"), "package b // sparse\n", 0o644)
	c, err := CaptureWorktree(t.Context(), CaptureOptions{Worktree: f.src})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Changes) != 1 || c.Changes[0].Path != "src/b.go" || c.Changes[0].Status != 'M' {
		t.Fatalf("changes = %+v, want only src/b.go", c.Changes)
	}
	if err := PushBranch(t.Context(), f.src, c, GitHubFileLimit); err != nil {
		t.Fatal(err)
	}
	if got := g(t, f.src, "status", "--porcelain"); got != "" {
		t.Fatalf("status after push:\n%s", got)
	}
	if got := g(t, f.src, "ls-files", "-t", "docs/old.md"); got != "S docs/old.md" {
		t.Fatalf("sparse entry after push = %q", got)
	}
}

// commit-tree ignores commit.gpgSign, unlike git commit, so a signing setup
// that needs a key or a prompt cannot block or fail a move.
func TestCaptureWithoutIdentityAndWithSigningOn(t *testing.T) {
	f := newFixture(t)
	empty := filepath.Join(f.tmp, "empty-gitconfig")
	writeFile(t, empty, "", 0o644)
	t.Setenv("GIT_CONFIG_GLOBAL", empty)
	g(t, f.src, "config", "user.useConfigOnly", "true")
	g(t, f.src, "config", "commit.gpgSign", "true")
	g(t, f.src, "config", "gpg.program", "false")
	writeFile(t, filepath.Join(f.src, "README.md"), "edited\n", 0o644)
	c, err := CaptureWorktree(t.Context(), CaptureOptions{Worktree: f.src})
	if err != nil {
		t.Fatal(err)
	}
	if got := g(t, f.src, "log", "-1", "--format=%an <%ae> %G?", c.Commit); got != "AO <ao@localhost> N" {
		t.Fatalf("author and signature = %q", got)
	}
}

// LFS objects would stay behind, since the push runs without the LFS hook,
// whether the pointer is in the WIP commit or a commit origin lacks.
func TestPushRefusesLFSPointers(t *testing.T) {
	for _, committed := range []bool{false, true} {
		f := newFixture(t)
		writeFile(t, filepath.Join(f.src, ".gitattributes"), "*.psd filter=lfs diff=lfs merge=lfs -text\n", 0o644)
		pointer := lfsPointerPrefix + "\noid sha256:" + strings.Repeat("ab", 32) + "\nsize 48213\n"
		writeFile(t, filepath.Join(f.src, "art/design.psd"), pointer, 0o644)
		if committed {
			g(t, f.src, "add", "-A")
			g(t, f.src, "commit", "-q", "-m", "design")
		}
		c, err := CaptureWorktree(t.Context(), CaptureOptions{Worktree: f.src})
		if err != nil {
			t.Fatal(err)
		}
		if committed != (c.Commit == "") {
			t.Fatalf("committed=%v: capture commit %q", committed, c.Commit)
		}
		if err := PushBranch(t.Context(), f.src, c, GitHubFileLimit); !errors.Is(err, ErrLFSObjects) || !strings.Contains(err.Error(), "art/design.psd") {
			t.Fatalf("committed=%v: push err = %v, want ErrLFSObjects naming art/design.psd", committed, err)
		}
		if got := g(t, f.origin, "rev-parse", "refs/heads/feat/x"); got != f.c1 {
			t.Fatalf("committed=%v: refused push still changed origin", committed)
		}
	}
}

func TestCaptureExcludeKeepsStagedChangesBehind(t *testing.T) {
	f := newFixture(t)
	writeFile(t, filepath.Join(f.src, "README.md"), "staged secret\n", 0o644)
	g(t, f.src, "add", "README.md")
	writeFile(t, filepath.Join(f.src, "big/data.bin"), randomBytes(t, 1<<20), 0o644)
	writeFile(t, filepath.Join(f.src, "src/b.go"), "package b // moves\n", 0o644)
	c, err := CaptureWorktree(t.Context(), CaptureOptions{Worktree: f.src, Exclude: []string{"README.md", "big"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Changes) != 1 || c.Changes[0].Path != "src/b.go" {
		t.Fatalf("changes = %+v, want only src/b.go", c.Changes)
	}
}

// AO sessions run in linked worktrees, whose index and MERGE_HEAD live under
// .git/worktrees/<name>, not in the main checkout.
func TestCaptureInLinkedWorktree(t *testing.T) {
	f := newFixture(t)
	session := filepath.Join(f.tmp, "session")
	g(t, f.src, "worktree", "add", "-q", "-b", "ao/session-1", session)
	writeFile(t, filepath.Join(session, "src/b.go"), "package b // session\n", 0o644)
	writeFile(t, filepath.Join(session, "new.txt"), "new\n", 0o644)
	before := stateOf(t, session)
	c, err := CaptureWorktree(t.Context(), CaptureOptions{Worktree: session})
	if err != nil {
		t.Fatal(err)
	}
	if c.Branch != "ao/session-1" || c.Base != f.c2 || len(c.Changes) != 2 {
		t.Fatalf("capture = %+v", c)
	}
	if after := stateOf(t, session); after != before {
		t.Fatalf("capture changed the linked worktree:\nbefore %+v\nafter  %+v", before, after)
	}

	// A merge stopped in the main checkout does not block the session.
	g(t, f.src, "switch", "-q", "-c", "other", "main")
	writeFile(t, filepath.Join(f.src, "src/c.go"), "package c // other\n", 0o644)
	g(t, f.src, "add", "src/c.go")
	g(t, f.src, "commit", "-q", "-m", "other")
	g(t, f.src, "switch", "-q", "feat/x")
	writeFile(t, filepath.Join(f.src, "src/c.go"), "package c // mine\n", 0o644)
	g(t, f.src, "commit", "-q", "-am", "mine")
	_, _ = gitTry(f.src, "merge", "-q", "other")
	if _, err := CaptureWorktree(t.Context(), CaptureOptions{Worktree: f.src}); !errors.Is(err, ErrOperationInProgress) {
		t.Fatalf("main checkout: err = %v, want ErrOperationInProgress", err)
	}
	if _, err := CaptureWorktree(t.Context(), CaptureOptions{Worktree: session}); err != nil {
		t.Fatalf("session blocked by the main checkout's merge: %v", err)
	}
}

// A retry after a push origin took but this side could not confirm must push
// the same commit, so the capture does not depend on the clock.
func TestCaptureIsRepeatable(t *testing.T) {
	f := newFixture(t)
	writeFile(t, filepath.Join(f.src, "README.md"), "edited\n", 0o644)
	var commits []string
	for _, now := range []string{"1700000000 +0000", "1800000000 +0000"} {
		t.Setenv("GIT_AUTHOR_DATE", now)
		t.Setenv("GIT_COMMITTER_DATE", now)
		c, err := CaptureWorktree(t.Context(), CaptureOptions{Worktree: f.src})
		if err != nil {
			t.Fatal(err)
		}
		commits = append(commits, c.Commit)
	}
	if commits[0] == "" || commits[0] != commits[1] {
		t.Fatalf("captures = %v, want one commit twice", commits)
	}
}
