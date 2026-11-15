//go:build unix

package kimchi

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestReadBoundedSystemPromptRejectsNonRegularFile(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "prompt.fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatalf("syscall.Mkfifo: %v", err)
	}

	_, err := readBoundedSystemPrompt(context.Background(), fifo)
	if err == nil {
		t.Fatal("expected error for non-regular file (FIFO), got nil")
	}
	if !contains(err.Error(), "not a regular file") {
		t.Fatalf("error should mention non-regular file; got %q", err.Error())
	}

	info, err := os.Stat(fifo)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatal("expected named pipe mode")
	}
}
