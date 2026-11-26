package scratch

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// An AO entry replaced between scan and removal is someone else's now: it is
// kept and reported dirty, and the marker that claimed it stays too.
func TestRemovePreservesEntryReplacedAfterScan(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".gitignore"), hookutil.GitignoreSentinel+"\n/.gitignore\n/hooks.json\n")
	writeFile(t, filepath.Join(root, "hooks.json"), "{}\n")

	contents, err := scanScratchContents(root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if contents.userWork {
		t.Fatal("scan saw user work in an AO-only folder")
	}
	if err := os.Remove(filepath.Join(root, "hooks.json")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "hooks.json", "notes.md"), "mine\n")

	if err := contents.remove(root); !errors.Is(err, ports.ErrWorkspaceDirty) {
		t.Fatalf("remove err = %v, want ErrWorkspaceDirty", err)
	}
	for _, kept := range []string{filepath.Join("hooks.json", "notes.md"), ".gitignore"} {
		if _, err := os.Stat(filepath.Join(root, kept)); err != nil {
			t.Fatalf("%s was not preserved: %v", kept, err)
		}
	}
}
