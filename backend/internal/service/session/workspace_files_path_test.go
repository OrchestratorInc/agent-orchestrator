package session

import (
	"runtime"
	"testing"
)

func TestCleanWorkspaceRelativePathKeepsBackslashOnPOSIX(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("backslash is a separator on Windows; this pins the POSIX behaviour")
	}

	got, err := cleanWorkspaceRelativePath(`dir/weird\name.txt`)
	if err != nil {
		t.Fatalf("cleanWorkspaceRelativePath: %v", err)
	}
	if want := `dir/weird\name.txt`; got != want {
		t.Fatalf("got %q, want %q; a backslash must stay part of the filename on this platform", got, want)
	}
}

func TestCleanWorkspaceRelativePathStillRejectsEscapes(t *testing.T) {
	for _, raw := range []string{"", "..", "../outside.txt", "/abs/path.txt"} {
		if _, err := cleanWorkspaceRelativePath(raw); err == nil {
			t.Fatalf("cleanWorkspaceRelativePath(%q) = nil error, want rejection", raw)
		}
	}
}
