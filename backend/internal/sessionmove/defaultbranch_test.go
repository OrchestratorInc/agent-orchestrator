package sessionmove

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestRefuseDefaultBranch(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// feat/x is neither origin's default nor the project's.
	if def, err := RefuseDefaultBranch(ctx, f.src, "auto"); err != nil || def != "main" {
		t.Fatalf("feat/x = %q, %v; want main, nil", def, err)
	}
	// The project's configured default branch is refused by name.
	if _, err := RefuseDefaultBranch(ctx, f.src, "feat/x"); !errors.Is(err, ErrDefaultBranch) {
		t.Fatalf("project default err = %v, want ErrDefaultBranch", err)
	}

	// On main, origin's default, with a dirty file: refused whether the clone
	// caches origin/HEAD or has to ask origin.
	g(t, f.src, "switch", "-q", "main")
	writeFile(t, filepath.Join(f.src, "README.md"), "dirty\n", 0o644)
	if _, err := RefuseDefaultBranch(ctx, f.src, ""); !errors.Is(err, ErrDefaultBranch) {
		t.Fatalf("cached origin/HEAD err = %v, want ErrDefaultBranch", err)
	}
	g(t, f.src, "remote", "set-head", "origin", "-d")
	if _, err := RefuseDefaultBranch(ctx, f.src, ""); !errors.Is(err, ErrDefaultBranch) {
		t.Fatalf("live origin HEAD err = %v, want ErrDefaultBranch", err)
	}
}

func TestRemoteErrorsAreClassified(t *testing.T) {
	for _, tc := range []struct {
		stdout, stderr string
		want           error
	}{
		{"", "remote: error: GH006: Protected branch update failed for refs/heads/main.\n ! [remote rejected] main -> main (protected branch hook declined)", ErrRemoteRejected},
		{"!\tabc:refs/heads/x\t[rejected] (fetch first)", "", ErrRemoteAhead},
		{"", "fatal: Authentication failed for 'https://github.com/acme/app.git/'", ErrRemoteAuth},
		{"", "git@github.com: Permission denied (publickey).\nfatal: Could not read from remote repository.", ErrRemoteAuth},
		{"", "fatal: unable to access 'https://github.com/acme/app.git/': Could not resolve host: github.com", ErrRemoteUnreachable},
	} {
		err := remoteError(&gitError{args: []string{"push"}, stderr: tc.stderr, err: errors.New("exit status 1")}, tc.stdout)
		if !errors.Is(err, tc.want) {
			t.Errorf("remoteError(%q, %q) = %v, want %v", tc.stdout, tc.stderr, err, tc.want)
		}
	}
	other := &gitError{args: []string{"push"}, stderr: "something else", err: errors.New("exit status 1")}
	if got := remoteError(other, ""); !errors.Is(got, other) || got.Error() != other.Error() {
		t.Errorf("unclassified error changed: %v", got)
	}
}
