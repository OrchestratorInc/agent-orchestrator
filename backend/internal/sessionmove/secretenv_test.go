package sessionmove

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestIsSecretEnvFileName(t *testing.T) {
	for name, want := range map[string]bool{
		".env": true, ".env.local": true, ".env.production.local": true, ".env.test": true, ".env..": true,
		".env.example": false, ".env.sample": false, ".env.template": false, ".env.": false, ".envrc": false,
		".env-local": false, "x.env": false, "env": false, ".env.lo cal": false, "README.md": false,
	} {
		if got := IsSecretEnvFileName(name); got != want {
			t.Errorf("IsSecretEnvFileName(%q) = %v, want %v", name, got, want)
		}
	}
}

// An untracked or newly staged secret environment file, and local edits to a
// committed one, never reach the WIP commit, the push or origin: they stay in
// the worktree, and the capture names them. A template such as .env.example
// moves like any other file.
func TestCaptureWithholdsSecretEnvFiles(t *testing.T) {
	f := newFixture(t)
	const secret = "AO_FAKE_SECRET=round-trip-not-a-secret"
	writeFile(t, filepath.Join(f.src, "deploy/.env.staging"), "A=1\n", 0o644)
	g(t, f.src, "add", "-f", "deploy/.env.staging")
	g(t, f.src, "commit", "-q", "-m", "staging env")
	base := g(t, f.src, "rev-parse", "HEAD")

	// Nothing ignores .env any more, so it is untracked rather than ignored.
	writeFile(t, filepath.Join(f.src, ".gitignore"), "*.log\n", 0o644)
	writeFile(t, filepath.Join(f.src, ".env"), secret+"\n", 0o644)
	writeFile(t, filepath.Join(f.src, ".env.local"), secret+"\n", 0o644)
	writeFile(t, filepath.Join(f.src, "config/.env.production"), secret+"\n", 0o644)
	writeFile(t, filepath.Join(f.src, "svc/.env.test"), secret+"\n", 0o644)
	g(t, f.src, "add", "svc/.env.test")
	writeFile(t, filepath.Join(f.src, "deploy/.env.staging"), "A=2\n"+secret+"\n", 0o644)
	writeFile(t, filepath.Join(f.src, ".env.example"), "AO_FAKE_SECRET=\n", 0o644)

	c, err := CaptureWorktree(t.Context(), CaptureOptions{Worktree: f.src})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".env", ".env.local", "config/.env.production", "deploy/.env.staging", "svc/.env.test"}
	if !reflect.DeepEqual(c.Withheld, want) {
		t.Fatalf("withheld = %v, want %v", c.Withheld, want)
	}
	for _, ch := range c.Changes {
		if IsSecretEnvFileName(filepath.Base(ch.Path)) {
			t.Fatalf("changes list %s", ch.Path)
		}
	}
	if err := PushBranch(t.Context(), f.src, c, GitHubFileLimit); err != nil {
		t.Fatal(err)
	}
	for _, repo := range []string{f.src, f.origin} {
		tip := g(t, repo, "rev-parse", "refs/heads/feat/x")
		if tip != c.Commit || g(t, repo, "rev-parse", tip+"^") != base {
			t.Fatalf("%s feat/x = %s, want the WIP commit %s on %s", repo, tip, c.Commit, base)
		}
		files := g(t, repo, "ls-tree", "-r", "--name-only", tip)
		for _, gone := range []string{".env", ".env.local", "config/.env.production", "svc/.env.test"} {
			if strings.Contains("\n"+files+"\n", "\n"+gone+"\n") {
				t.Fatalf("%s's tree has %s:\n%s", repo, gone, files)
			}
		}
		if !strings.Contains(files, ".env.example") || g(t, repo, "show", tip+":deploy/.env.staging") != "A=1" {
			t.Fatalf("%s's tree lacks the template or changed the committed env file:\n%s", repo, files)
		}
		if out, err := gitTry(repo, "grep", "-q", "round-trip-not-a-secret", tip); err == nil {
			t.Fatalf("%s's tree holds the secret: %s", repo, out)
		}
	}
	// They stay on this computer, as they were.
	for _, p := range []string{".env", ".env.local", "config/.env.production", "svc/.env.test"} {
		if data, err := os.ReadFile(filepath.Join(f.src, p)); err != nil || !strings.Contains(string(data), secret) {
			t.Fatalf("%s here = %q, %v", p, data, err)
		}
	}
	// A second capture of the same worktree is clean: nothing left that moves.
	again, err := CaptureWorktree(t.Context(), CaptureOptions{Worktree: f.src})
	if err != nil || again.Commit != "" || len(again.Withheld) != len(want) {
		t.Fatalf("second capture = %+v, %v; want no commit and the same files withheld", again, err)
	}

	// The archive keeps this worktree for those files; it must not hold the
	// branch, so a move back here can place it.
	if err := ReleaseBranch(t.Context(), f.src); err != nil {
		t.Fatal(err)
	}
	if list := g(t, f.src, "worktree", "list", "--porcelain"); strings.Contains(list, "branch refs/heads/feat/x") {
		t.Fatalf("feat/x is still checked out:\n%s", list)
	}
	if data, err := os.ReadFile(filepath.Join(f.src, ".env")); err != nil || !strings.Contains(string(data), secret) {
		t.Fatalf(".env after release = %q, %v", data, err)
	}
	if err := PlaceBranch(t.Context(), f.src, "feat/x", c.Commit); err != nil {
		t.Fatalf("PlaceBranch after release: %v", err)
	}
}
