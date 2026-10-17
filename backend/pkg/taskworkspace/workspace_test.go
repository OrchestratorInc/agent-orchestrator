package taskworkspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSharedWorkspaceNamingAndRecovery(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	repo := filepath.Join(root, "repository")
	git := func(dir string, args ...string) string {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", dir}, args...)...)
		command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+filepath.Join(root, "absent"), "GIT_CONFIG_NOSYSTEM=1")
		out, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	git(repo, "init", "-b", "main")
	git(repo, "-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "--allow-empty", "-m", "initial")
	options := Options{RepositoryPath: repo, ManagedRoot: filepath.Join(root, "worktrees"), ProjectID: "project", SessionID: "project-1"}
	w, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	info, err := w.Create(ctx, "ao/project-1/root", "main")
	if err != nil {
		t.Fatal(err)
	}
	occupied := filepath.Join(options.ManagedRoot, options.ProjectID, "login-redirect")
	if err := os.Mkdir(occupied, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(occupied, "keep"), []byte("preserved"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(repo, "update-ref", "refs/remotes/origin/ao/login-redirect-2/root", "HEAD")
	info, err = w.Name(ctx, info, "ao/login-redirect/root", "login-redirect")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(info.Path) != "login-redirect-3" || info.Branch != "ao/login-redirect-3/root" {
		t.Fatalf("named = %+v", info)
	}
	if got := git(info.Path, "branch", "--show-current"); got != info.Branch {
		t.Fatalf("branch = %q", got)
	}
	w, err = New(options)
	if err != nil {
		t.Fatal(err)
	}
	recovered, found, err := w.Recover(ctx)
	if err != nil || !found || recovered != info {
		t.Fatalf("recovery = %+v/%v: %v", recovered, found, err)
	}
	if err := w.Complete(ctx, recovered); err != nil {
		t.Fatal(err)
	}
	if _, found, err := w.Recover(ctx); err != nil || found {
		t.Fatalf("acknowledged recovery = %v: %v", found, err)
	}
	if err := os.WriteFile(filepath.Join(info.Path, "dirty"), []byte("work"), 0o600); err != nil {
		t.Fatal(err)
	}
	restored, err := w.Restore(ctx, info, "main")
	if err != nil || restored != info {
		t.Fatalf("restore = %+v: %v", restored, err)
	}
	if got, err := os.ReadFile(filepath.Join(occupied, "keep")); err != nil || string(got) != "preserved" {
		t.Fatalf("occupied path changed: %q/%v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(info.Path, "dirty")); err != nil || string(got) != "work" {
		t.Fatalf("work changed: %q/%v", got, err)
	}
}
