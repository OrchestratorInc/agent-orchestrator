package worker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtraRepoDirName(t *testing.T) {
	cases := map[string]string{
		"https://github.com/Pritom14/ChartSnip": "ChartSnip",
		"https://github.com/owner/repo.git":     "repo",
		"https://github.com/owner/repo/":        "repo",
		"https://github.com/owner/weird name":   "weird-name",
		"":                                      "repo",
	}
	for in, want := range cases {
		if got := ExtraRepoDirName(in); got != want {
			t.Errorf("ExtraRepoDirName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExtraRepoPathIsSiblingOfWorkspace(t *testing.T) {
	got := ExtraRepoPath("/workspace/repository", "https://github.com/Pritom14/ChartSnip")
	want := "/workspace/ChartSnip"
	if got != want {
		t.Fatalf("ExtraRepoPath = %q, want %q (sibling of the primary checkout)", got, want)
	}
}

// An additional repository whose folder the worker cannot create (a folder
// beside the working folder that the template made and the bootstrap did not
// prepare, typically for a repository added after the session was created) is
// reported with why, so it is skipped with an explicit warning.
func TestExtraRepoFolderBlocked(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root may write anywhere")
	}
	parent := t.TempDir()
	existing := filepath.Join(parent, "alice")
	if err := os.Mkdir(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := ExtraRepoFolderBlocked(filepath.Join(parent, "billing")); got != "" {
		t.Fatalf("a writable parent is blocked: %s", got)
	}
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })
	if got := ExtraRepoFolderBlocked(existing); got != "" {
		t.Fatalf("an existing folder is blocked: %s", got)
	}
	if got := ExtraRepoFolderBlocked(filepath.Join(parent, "billing")); !strings.Contains(got, parent+" does not let AO's worker user create billing") {
		t.Fatalf("blocked = %q", got)
	}
}
