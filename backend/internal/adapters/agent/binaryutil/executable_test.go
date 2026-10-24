package binaryutil

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeDiscoveryLauncher(t *testing.T, root, name, body string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDiscoveryExecutableLimitsTotalFiles(t *testing.T) {
	for _, files := range []int{discoveryLauncherFileLimit, discoveryLauncherFileLimit + 1} {
		t.Run(fmt.Sprint(files), func(t *testing.T) {
			root := t.TempDir()
			var body strings.Builder
			for i := 1; i < files; i++ {
				name := fmt.Sprintf("bridge-%d.cmd", i)
				writeDiscoveryLauncher(t, root, name, "@echo off\n")
				fmt.Fprintf(&body, "@call \"%%~dp0%s\" %%*\n", name)
			}
			launcher := writeDiscoveryLauncher(t, root, "grok.cmd", body.String())
			_, err := VerifyExecutable(context.Background(), "grok", launcher)
			if files <= discoveryLauncherFileLimit {
				if err != nil {
					t.Fatalf("launcher within file budget rejected: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "total file inspection limit") {
				t.Fatalf("verification = %v, want total file limit rejection", err)
			}
		})
	}
}

func TestDiscoveryExecutableReusesSharedTargets(t *testing.T) {
	root := t.TempDir()
	// Repeated quoted commands and branches must reuse completed results rather
	// than reread each file.
	shared := strings.Repeat("\"%~dp0grok.exe\" %*\n", discoveryLauncherFileLimit+1)
	writeDiscoveryLauncher(t, root, "grok.exe", "MZ fixture")
	writeDiscoveryLauncher(t, root, "left.cmd", shared)
	writeDiscoveryLauncher(t, root, "right.cmd", shared)
	launcher := writeDiscoveryLauncher(t, root, "grok.cmd", "@call \"%~dp0left.cmd\" %*\n@call \"%~dp0right.cmd\" %*\n")
	if _, err := VerifyExecutable(context.Background(), "grok", launcher); err != nil {
		t.Fatalf("repeated shared targets rejected: %v", err)
	}
}

func TestDiscoveryExecutableAllowsNativeManagerSymlinks(t *testing.T) {
	for _, tc := range []struct{ name, target, body string }{
		{"mise", ".local/bin/mise", "\x7fELF\x00fixture"},
		{"volta", ".volta/bin/volta-shim", "\xcf\xfa\xed\xfe\x00fixture"},
		{"renamed native", "releases/release-9f31b7", "MZ fixture"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			target := writeDiscoveryLauncher(t, root, tc.target, tc.body)
			name := "grok"
			if runtime.GOOS == "windows" {
				name += ".exe"
			}
			link := filepath.Join(root, name)
			if err := os.Symlink(target, link); err != nil {
				if runtime.GOOS == "windows" {
					t.Skipf("symlink creation unavailable: %v", err)
				}
				t.Fatal(err)
			}
			verified, err := VerifyExecutable(context.Background(), "grok", link)
			if err != nil || verified != link {
				t.Fatalf("native manager verification = %q, %v, want launcher %q", verified, err, link)
			}
			if _, err := VerifyExecutable(context.Background(), "grok", target); err == nil {
				t.Fatal("unidentified native target accepted without an agent entry point")
			}
			inspection := &discoveryInspection{active: map[string]bool{}, completed: map[string]discoveryInspectionResult{}}
			if ok, err := inspectDiscoveryExecutable(context.Background(), "grok", link, inspection); err != nil || !ok {
				t.Fatalf("agent alias rejected: %v", err)
			}
			if ok, err := inspectDiscoveryExecutable(context.Background(), "grok", target, inspection); err != nil || ok {
				t.Fatalf("cached agent alias identity leaked to unidentified target: %t, %v", ok, err)
			}
		})
	}
}
