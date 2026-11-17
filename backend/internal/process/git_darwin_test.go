//go:build darwin

package process

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitSkipsTheXcrunShimWhenXcrunFindsGit(t *testing.T) {
	resolved := resolveRealGit()
	if resolved == "" {
		t.Skip("git on PATH is not the xcrun shim, or xcrun cannot find git")
	}
	if got := gitPath("git", resolved); got != resolved {
		t.Fatalf("gitPath = %q, want %q", got, resolved)
	}
	out, err := CommandContext(context.Background(), resolved, "--version").Output()
	if err != nil || !strings.HasPrefix(string(out), "git version") {
		t.Fatalf("%s --version = %q, %v", resolved, out, err)
	}
}

func TestGitFallsBackToTheShimWithoutAResolvedBinary(t *testing.T) {
	for _, resolved := range []string{"", filepath.Join(t.TempDir(), "removed-git")} {
		if got := gitPath("git", resolved); got != "git" {
			t.Fatalf("gitPath(%q) = %q, want git", resolved, got)
		}
	}
}

func TestGitCommandRuns(t *testing.T) {
	out, err := CommandContext(context.Background(), "git", "--version").Output()
	if err != nil || !strings.HasPrefix(string(out), "git version") {
		t.Fatalf("git --version = %q, %v", out, err)
	}
}
