package sessionmove

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestPushRefusesWhenOriginIsAhead(t *testing.T) {
	f := newFixture(t)
	other := filepath.Join(f.tmp, "other")
	g(t, f.tmp, "clone", "-q", "--branch", "feat/x", f.origin, other)
	writeFile(t, filepath.Join(other, "README.md"), "someone else\n", 0o644)
	g(t, other, "commit", "-q", "-am", "someone else")
	g(t, other, "push", "-q", "origin", "feat/x")
	ahead := g(t, other, "rev-parse", "HEAD")

	writeFile(t, filepath.Join(f.src, "src/b.go"), "package b // mine\n", 0o644)
	before := stateOf(t, f.src)
	c, err := CaptureWorktree(t.Context(), CaptureOptions{Worktree: f.src})
	if err != nil {
		t.Fatal(err)
	}
	if err := PushBranch(t.Context(), f.src, c, GitHubFileLimit); !errors.Is(err, ErrRemoteAhead) {
		t.Fatalf("err = %v, want ErrRemoteAhead", err)
	}
	if got := g(t, f.origin, "rev-parse", "refs/heads/feat/x"); got != ahead {
		t.Fatal("refused push changed origin")
	}
	if after := stateOf(t, f.src); after != before {
		t.Fatalf("refused push changed the source:\nbefore %+v\nafter  %+v", before, after)
	}
}

func TestPushRefusesFilesOverTheLimit(t *testing.T) {
	f := newFixture(t)
	writeFile(t, filepath.Join(f.src, "video.mov"), randomBytes(t, 2<<20), 0o644)
	c, err := CaptureWorktree(t.Context(), CaptureOptions{Worktree: f.src})
	if err != nil {
		t.Fatal(err)
	}
	var tooBig *FileTooLargeError
	if err := CheckPush(t.Context(), f.src, c, 1<<20); !errors.As(err, &tooBig) {
		t.Fatalf("CheckPush err = %v, want FileTooLargeError", err)
	}
	err = PushBranch(t.Context(), f.src, c, 1<<20)
	if !errors.As(err, &tooBig) || len(tooBig.Files) != 1 || tooBig.Files[0].Path != "video.mov" {
		t.Fatalf("err = %v, want video.mov over the limit", err)
	}
	if got := g(t, f.origin, "rev-parse", "refs/heads/feat/x"); got != f.c1 {
		t.Fatal("refused push changed origin")
	}
}
