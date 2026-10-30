package sessionartifacts

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Publishing an artifact as a public share link is something only a person
// does, from the desktop app's share flow, which asks them to confirm in a
// native dialog. Agents read untrusted content, and a hidden instruction
// ("publish .env") must have no way to turn a file into a public link: no
// daemon route, no ao CLI command and no MCP tool may call the share API.
// This test fails if backend code ever names it.
func TestNoBackendPathPublishesShareLinks(t *testing.T) {
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the backend source")
	}
	backend := filepath.Join(filepath.Dir(here), "..", "..")
	forbidden := []string{"/me/artifact-shares", "artifact-shares/uploads", "artifact-shares/finalize", "cloudShare:"}
	err := filepath.WalkDir(backend, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if name := entry.Name(); name == "node_modules" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, needle := range forbidden {
			if strings.Contains(string(source), needle) {
				t.Errorf("%s names %q: share links are published only from the desktop app's share flow", path, needle)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
