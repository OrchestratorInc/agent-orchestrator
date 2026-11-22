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
	// A link inside the worktree that points out of it.
	if err := os.Symlink(elsewhere, filepath.Join(workspace, "external")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(elsewhere, "shared.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A link outside the worktree into a nested worktree directory, so a ".."
	// after it lands back inside the worktree, not beside the link.
	if err := os.MkdirAll(filepath.Join(workspace, "src", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	aliasDir := t.TempDir()
	if err := os.Symlink(filepath.Join(workspace, "src", "deep"), filepath.Join(aliasDir, "link")); err != nil {
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
		// A deleted file whose parent directories went with it, reported
		// through the other spelling of the worktree.
		{"deleted directory through a symlink", workspace, filepath.Join(link, "removed", "nested", "gone.go"), false},
		{"deleted directory with the root through a symlink", link, filepath.Join(workspace, "removed", "nested", "gone.go"), false},
		{"artifact", workspace, filepath.Join(artifacts, "report.html"), false},
		{"outside", workspace, filepath.Join(elsewhere, "memory", "MEMORY.md"), true},
		{"through an outward link", workspace, filepath.Join(workspace, "external", "shared.go"), true},
		{"new file through an outward link", workspace, filepath.Join(workspace, "external", "new.go"), true},
		// Concatenated, not filepath.Join, which would collapse the ".." first.
		{"parent of a link into the worktree", workspace, aliasDir + "/link/../app.go", false},
		{"parent of a link out of the worktree", workspace, workspace + "/external/../app.go", true},
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
