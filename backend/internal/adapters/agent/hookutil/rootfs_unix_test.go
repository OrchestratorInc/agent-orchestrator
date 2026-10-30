//go:build !windows

package hookutil

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestEnsureWorkspaceGitignoreReplacesFIFOWithoutHanging(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- EnsureWorkspaceGitignore(dir, "hooks.json") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("EnsureWorkspaceGitignore hung on a FIFO")
	}
	if fi, err := os.Lstat(path); err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("FIFO not replaced: %v %v", fi, err)
	}
}
