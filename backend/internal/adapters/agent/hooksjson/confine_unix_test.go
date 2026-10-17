//go:build !windows

package hooksjson

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestInstallReplacesFIFOSettingsFile(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, ".claude"), 0o750); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(ws, ".claude", "settings.local.json")
	if err := syscall.Mkfifo(target, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- confineManager().Install(t.Context(), ws) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Install hung on FIFO")
	}
	if fi, err := os.Lstat(target); err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("FIFO not replaced: %v %v", fi, err)
	}
}
