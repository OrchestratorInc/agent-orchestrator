//go:build darwin

package process

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func TestGitSkipsTheXcrunShim(t *testing.T) {
	path, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	cmd := CommandContext(context.Background(), "git", "--version")
	if path == "/usr/bin/git" && cmd.Path == path {
		t.Fatalf("git runs through the xcrun shim %s", cmd.Path)
	}
	out, err := cmd.Output()
	if err != nil || !strings.HasPrefix(string(out), "git version") {
		t.Fatalf("%s --version = %q, %v", cmd.Path, out, err)
	}
}
