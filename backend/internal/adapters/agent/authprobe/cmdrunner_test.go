package authprobe

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Auth probes run npm `#!/usr/bin/env node` launchers. CmdRunner must give
// them a Node manager's runtime when the daemon PATH omits it.
func TestCmdRunnerFindsNodeOutsideDaemonPATH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("npm env-node launcher is Unix-specific")
	}
	home := t.TempDir()
	nodeDir := filepath.Join(home, ".nvm", "versions", "node", "v22.11.0", "bin")
	if err := os.MkdirAll(nodeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nodeDir, "node"), []byte("#!/bin/sh\necho logged in\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(home, "npm-global", "bin", "agent")
	if err := os.MkdirAll(filepath.Dir(launcher), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcher, []byte("#!/usr/bin/env node\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())

	out, err := CmdRunner(context.Background(), launcher, "auth", "status")
	if err != nil {
		t.Fatalf("CmdRunner = %v (output %q), want the npm launcher to find node", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "logged in" {
		t.Fatalf("output = %q, want %q", got, "logged in")
	}
}
