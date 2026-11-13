package gemini

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestVersionProbeAcceptsSupportedCLIAndRejectsOldCLI(t *testing.T) {
	for _, tc := range []struct {
		version   string
		wantError bool
	}{{"0.60.0", false}, {"0.59.9", true}, {"unrecognized", true}} {
		t.Run(tc.version, func(t *testing.T) {
			name, body := "gemini", "#!/bin/sh\necho "+tc.version+"\n"
			if runtime.GOOS == "windows" {
				name, body = "gemini.cmd", "@echo off\r\necho "+tc.version+"\r\n"
			}
			binary := filepath.Join(t.TempDir(), name)
			if err := os.WriteFile(binary, []byte(body), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := checkVersion(context.Background(), binary); (err != nil) != tc.wantError {
				t.Fatalf("checkVersion = %v", err)
			}
		})
	}
}

// An npm-installed Gemini is a `#!/usr/bin/env node` launcher. The probe must
// find a Node manager's runtime when the daemon PATH omits it, as a Dock-
// launched daemon's PATH does when the login-shell probe fails.
func TestVersionProbeFindsNodeOutsideDaemonPATH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("npm env-node launcher is Unix-specific")
	}
	home := t.TempDir()
	nodeDir := filepath.Join(home, ".nvm", "versions", "node", "v22.11.0", "bin")
	if err := os.MkdirAll(nodeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nodeDir, "node"), []byte("#!/bin/sh\necho 0.61.0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(home, "npm-global", "bin", "gemini")
	if err := os.MkdirAll(filepath.Dir(launcher), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcher, []byte("#!/usr/bin/env node\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())

	if err := checkVersion(context.Background(), launcher); err != nil {
		t.Fatalf("checkVersion = %v, want the npm launcher to find node", err)
	}
}
