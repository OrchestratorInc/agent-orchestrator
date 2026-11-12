package chat

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathOutsideSession(t *testing.T) {
	workspace := t.TempDir()
	artifacts := t.TempDir()
	elsewhere := t.TempDir()
	// The worktree reached through a symlink still owns its files.
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(workspace, link); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, "src"), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name, workspace, path string
		want                  bool
	}{
		{"relative", workspace, "src/a.go", false},
		{"inside", workspace, filepath.Join(workspace, "src", "a.go"), false},
		{"worktree root", workspace, workspace, false},
		{"through a symlink", workspace, filepath.Join(link, "src", "gone.go"), false},
		{"artifact", workspace, filepath.Join(artifacts, "report.html"), false},
		{"outside", workspace, filepath.Join(elsewhere, "memory", "MEMORY.md"), true},
		{"sibling with a shared prefix", workspace, workspace + "-other/a.go", true},
		{"unknown worktree", "", filepath.Join(elsewhere, "a.go"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := pathOutsideSession(tc.workspace, artifacts, tc.path); got != tc.want {
				t.Errorf("pathOutsideSession(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}
