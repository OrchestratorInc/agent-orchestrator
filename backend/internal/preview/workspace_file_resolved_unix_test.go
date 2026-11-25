//go:build unix

package preview

import (
	"os"
	"path/filepath"
	"testing"
)

// The resolved path names the opened file's own bytes: a symlink repointed
// between the open and the lookup names nothing.
func TestResolvedPathNamesOnlyTheOpenedFile(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"docs/a.png", "private/customer.png"} {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "docs", "link.png")
	if err := os.Symlink("a.png", link); err != nil {
		t.Fatal(err)
	}
	file, info, clean, err := OpenWorkspaceFile(root, "docs/link.png")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if got, ok := ResolvedPath(root, clean, info); !ok || got != "docs/a.png" {
		t.Fatalf("ResolvedPath = %q, %v; want docs/a.png", got, ok)
	}

	// Repointed after the open: the open file is still docs/a.png, so the
	// private file the link now names must not be reported.
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "private", "customer.png"), link); err != nil {
		t.Fatal(err)
	}
	if got, ok := ResolvedPath(root, clean, info); ok {
		t.Fatalf("ResolvedPath = %q after the link was repointed; want none", got)
	}
}
