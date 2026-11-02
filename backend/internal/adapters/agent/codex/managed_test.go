package codex

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
)

// The Chat driver starts Codex directly, so AO's managed .cmd launcher must
// resolve to the native executable in the current managed release.
func TestResolveNativeWindowsCodexUnwrapsTheManagedLauncher(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	binaryutil.SetManagedBinDir(binDir)
	t.Cleanup(func() { binaryutil.SetManagedBinDir("") })

	launcher := filepath.Join(binDir, "codex.cmd")
	writeFile(t, launcher, "@\"%~dp0..\\codex\\current\\codex.cmd\" %*\r\n")
	if got := resolveNativeWindowsCodex(launcher, "windows"); got != launcher {
		t.Fatalf("without a native executable, resolved %q, want the launcher %q", got, launcher)
	}

	want := filepath.Join(root, "codex", "current", "node_modules", "@openai", "codex", "node_modules",
		"@openai", "codex-win32-x64", "vendor", "x86_64-pc-windows-msvc", "bin", "codex.exe")
	writeFile(t, want, "native codex")
	if got := resolveNativeWindowsCodex(launcher, "windows"); got != want {
		t.Fatalf("resolved %q, want the native executable in the current release %q", got, want)
	}
	if got := resolveNativeWindowsCodex(launcher, "linux"); got != launcher {
		t.Fatalf("non-Windows resolution = %q, want the unchanged launcher %q", got, launcher)
	}
}

// A shim outside AO's launcher directory keeps its own npm prefix lookup.
func TestWindowsNativeCodexCandidatesIgnoreTheManagedReleaseForOtherShims(t *testing.T) {
	binaryutil.SetManagedBinDir(filepath.Join(t.TempDir(), "bin"))
	t.Cleanup(func() { binaryutil.SetManagedBinDir("") })

	shim := filepath.Join("prefix", "codex.cmd")
	got := windowsNativeCodexCandidatesForShim(shim)
	want := windowsNativeCodexCandidatesInPrefix("prefix")
	if len(got) != len(want) {
		t.Fatalf("candidates = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidates = %#v, want %#v", got, want)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
