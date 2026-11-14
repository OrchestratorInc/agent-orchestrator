//go:build windows

package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceRepoUnavailableWindowsAbsoluteBackpointer(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "worktree with spaces")
	gitdir := filepath.Join(parent, "metadata with spaces", "worktrees", "session")
	common := filepath.Join(parent, "owning repo", ".git")
	for _, dir := range []string{root, gitdir, common} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: "+gitdir+"\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	relativeCommon, err := filepath.Rel(gitdir, common)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitdir, "commondir"), []byte(relativeCommon+"\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if workspaceRepoUnavailable(root) {
		t.Fatal("existing common repository classified as missing")
	}
	if err := os.RemoveAll(common); err != nil {
		t.Fatal(err)
	}
	if !workspaceRepoUnavailable(root) {
		t.Fatal("missing common repository was not detected through Windows absolute gitdir")
	}
}
