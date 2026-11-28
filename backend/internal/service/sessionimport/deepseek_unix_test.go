//go:build !windows

package sessionimport

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestDeepSeekDiscoveryDoesNotOpenSessionsFIFO(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sessions")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	// A writer can release a regressed blocking open even on the failure path.
	t.Cleanup(func() {
		fd, err := unix.Open(path, unix.O_RDWR|unix.O_NONBLOCK, 0)
		if err == nil {
			_ = unix.Close(fd)
		}
	})
	done := make(chan error, 1)
	go func() {
		_, err := discoverDeepSeek(context.Background(), root)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("native scan blocked opening a FIFO")
	}
}
