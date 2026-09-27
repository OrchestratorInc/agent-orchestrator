package reasonix

import (
	"os"
	"path/filepath"
	"testing"
)

func TestContainsFlag(t *testing.T) {
	for _, tc := range []struct {
		help string
		want bool
	}{
		{"reasonix [--model NAME] [--permission-mode MODE]", true},
		{"--model=NAME", true},
		{"--model NAME", true},
		{"--model-file PATH", false},
		{"--other-model NAME", false},
	} {
		if got := containsFlag(tc.help, "--model"); got != tc.want {
			t.Errorf("containsFlag(%q) = %v, want %v", tc.help, got, tc.want)
		}
	}
}

func TestWindowsNPMPayload(t *testing.T) {
	for _, tc := range []struct {
		name, arch, npmArch string
		local, nested       bool
	}{
		{"global nested x64", "amd64", "x64", false, true},
		{"global hoisted arm64", "arm64", "arm64", false, false},
		{"local nested arm64", "arm64", "arm64", true, true},
		{"local hoisted x64", "amd64", "x64", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			shim := filepath.Join(root, "reasonix.cmd")
			modules := filepath.Join(root, "node_modules")
			if tc.local {
				shim = filepath.Join(modules, ".bin", "reasonix.cmd")
			}
			if tc.nested {
				modules = filepath.Join(modules, "reasonix", "node_modules")
			}
			payload := filepath.Join(modules, "@reasonix", "cli-win32-"+tc.npmArch, "bin", "reasonix.exe")
			if err := os.MkdirAll(filepath.Dir(payload), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(payload, []byte("native test fixture"), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := nativeBinaryPath(shim, "windows", tc.arch)
			if err != nil || got != payload {
				t.Fatalf("path = %q, err = %v; want native payload %q", got, err, payload)
			}
		})
	}
	shim := filepath.Join(t.TempDir(), "reasonix.cmd")
	if got, err := nativeBinaryPath(shim, "windows", "amd64"); err == nil || got != "" {
		t.Fatalf("missing native package accepted: %q, %v", got, err)
	}
	for _, platform := range []string{"darwin", "linux"} {
		if got, err := nativeBinaryPath(shim, platform, "arm64"); err != nil || got != shim {
			t.Fatalf("non-Windows path changed: %q, %v", got, err)
		}
	}
	exe := filepath.Join(t.TempDir(), "reasonix.exe")
	if got, err := nativeBinaryPath(exe, "windows", "arm64"); err != nil || got != exe {
		t.Fatalf("native executable changed: %q, %v", got, err)
	}
}
