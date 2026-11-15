//go:build unix

package preview

import (
	"errors"
	"io/fs"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestOpenWorkspaceFileRefusesAFIFOWithoutBlocking(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "index.html"), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		file, _, _, err := OpenWorkspaceFile(dir, "index.html")
		if file != nil {
			_ = file.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("err = %v, want fs.ErrNotExist", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OpenWorkspaceFile blocked on a FIFO")
	}
	if _, ok := EntryAtPath(dir, "index.html"); ok {
		t.Fatal("EntryAtPath accepted a FIFO")
	}
}
