//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestSandboxRelayRefusesFIFORequestFile(t *testing.T) {
	dir := newRelayDir(t)
	calls := startRelay(t, dir)
	tmp := filepath.Join(dir, "req", "2-2-2.tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(tmp, "argv"), []byte("hooks\x00a\x00b\x00"), 0o600)
	if err := syscall.Mkfifo(filepath.Join(tmp, "stdin"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, "req", "2-2-2")); err != nil {
		t.Fatal(err)
	}
	requireRefused(t, dir, "2-2-2", "malformed")
	requireNoCall(t, calls)
}
