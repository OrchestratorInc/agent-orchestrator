//go:build unix

package claudecode

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// A planted FIFO must not hang PrepareSandbox; it is replaced.
func TestPrepareSandboxReplacesConfigFIFO(t *testing.T) {
	project := t.TempDir()
	configPath := filepath.Join(project, "claude", ".claude.json")
	_ = os.MkdirAll(filepath.Dir(configPath), 0o700)
	if err := syscall.Mkfifo(configPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (&Plugin{}).PrepareSandbox(map[string]string{"ANTHROPIC_API_KEY": "sk"}, t.TempDir(), project, "/w/s1"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	assertSandboxConfigSeeded(t, raw, "/w/s1")
}
