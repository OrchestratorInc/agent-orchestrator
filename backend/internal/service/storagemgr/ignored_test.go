package storagemgr

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
)

func TestKeptIgnoredListsOnlyFilesThatCannotBeRebuilt(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".gitignore"),
		".env\nlocal.db\nnode_modules/\ndist/\nbuild/\n.DS_Store\nuploads/\n/.ao/attachments/\nshared.env\n")
	writeFile(t, filepath.Join(dir, "src", "app.js"), "app")
	gitRun(t, dir, "init", "-q")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "-c", "user.name=AO test", "-c", "user.email=test@example.invalid", "commit", "-q", "-m", "init")

	// Lost for good if the worktree goes.
	writeFile(t, filepath.Join(dir, ".env"), "SECRET=1")
	writeFile(t, filepath.Join(dir, "local.db"), "rows")
	writeFile(t, filepath.Join(dir, "uploads", "photo.png"), "image")
	writeFile(t, filepath.Join(dir, "build", "server.key"), "key") // a build folder holding a secret
	// Come back on their own.
	writeFile(t, filepath.Join(dir, "node_modules", "dep", "index.js"), "dependency")
	writeFile(t, filepath.Join(dir, "dist", "app.js"), "built")
	writeFile(t, filepath.Join(dir, ".DS_Store"), "junk")
	writeFile(t, filepath.Join(dir, ".ao", "attachments", "attachment-1.png"), "projection")
	writeFile(t, filepath.Join(dir, ".claude", ".gitignore"), hookutil.GitignoreSentinel+"\n/.gitignore\n/settings.local.json\n")
	writeFile(t, filepath.Join(dir, ".claude", "settings.local.json"), "{}")
	if err := os.Symlink(filepath.Join(dir, ".env"), filepath.Join(dir, "shared.env")); err != nil {
		t.Logf("symlinks unavailable, link case not covered: %v", err)
	}

	kept, err := keptIgnored(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".env", "build/", "local.db", "uploads/"}
	if !reflect.DeepEqual(kept, want) {
		t.Fatalf("kept = %q, want %q", kept, want)
	}
}

func TestKeptIgnoredTrustsOnlyMarkedControlFiles(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	// The same names under a user's own .gitignore are the user's files.
	writeFile(t, filepath.Join(dir, ".gitignore"), "/.claude/settings.local.json\n")
	gitRun(t, dir, "init", "-q")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "-c", "user.name=AO test", "-c", "user.email=test@example.invalid", "commit", "-q", "-m", "init")
	writeFile(t, filepath.Join(dir, ".claude", "settings.local.json"), "{}")

	kept, err := keptIgnored(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{".claude/settings.local.json"}; !reflect.DeepEqual(kept, want) {
		t.Fatalf("kept = %q, want %q", kept, want)
	}
}

func TestDescribeKept(t *testing.T) {
	for _, tc := range []struct {
		kept    []string
		unknown bool
		want    string
	}{
		{nil, false, ""},
		{[]string{".env"}, false, "holds ignored files that cannot be rebuilt: .env"},
		{[]string{"a", "b", "c", "d", "e"}, false, "holds ignored files that cannot be rebuilt: a, b, c and 2 more"},
		{nil, true, "its ignored files could not be checked"},
	} {
		if got := describeKept(tc.kept, tc.unknown); got != tc.want {
			t.Fatalf("describeKept(%q, %v) = %q, want %q", tc.kept, tc.unknown, got, tc.want)
		}
	}
}
