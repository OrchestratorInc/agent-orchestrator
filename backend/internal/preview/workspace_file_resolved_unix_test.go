//go:build linux || darwin

package preview

import (
	"os"
	"path/filepath"
	"testing"
)

// The resolved path is read from the open handle, so it names the file whose
// bytes are served however its symlink is repointed after the open.
func TestResolvedPathNamesTheOpenedFile(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"docs/a.png", "docs/b b.png", "private/customer.png"} {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "docs", "link.png")
	repoint := func(target string) {
		t.Helper()
		_ = os.Remove(link)
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	resolved := func(asset string, afterOpen func()) string {
		t.Helper()
		file, _, _, err := OpenWorkspaceFile(root, asset)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = file.Close() }()
		afterOpen()
		got, ok := ResolvedPath(root, file)
		if !ok {
			t.Fatalf("ResolvedPath(%s) gave none", asset)
		}
		return got
	}
	nothing := func() {}

	if got := resolved("docs/b b.png", nothing); got != "docs/b%20b.png" {
		t.Fatalf("plain file = %q", got)
	}
	repoint("a.png")
	if got := resolved("docs/link.png", nothing); got != "docs/a.png" {
		t.Fatalf("link = %q, want docs/a.png", got)
	}
	// Repointed after the open: the bytes served are still a.png's.
	if got := resolved("docs/link.png", func() { repoint(filepath.Join("..", "private", "customer.png")) }); got != "docs/a.png" {
		t.Fatalf("link repointed after the open = %q, want docs/a.png", got)
	}
	// Opened while it points outside the folder, then pointed back and a.png
	// itself swapped for a link to the private file: the private file is
	// served, so it is the path named.
	repoint(filepath.Join("..", "private", "customer.png"))
	if got := resolved("docs/link.png", func() {
		repoint("a.png")
		a := filepath.Join(root, "docs", "a.png")
		_ = os.Remove(a)
		if err := os.Symlink(filepath.Join("..", "private", "customer.png"), a); err != nil {
			t.Fatal(err)
		}
	}); got != "private/customer.png" {
		t.Fatalf("opened private file = %q, want private/customer.png", got)
	}
}
