//go:build !windows

package process

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestGroupOpenFilesOfOwnGroup(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "held.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	paths, err := GroupOpenFiles(context.Background(), syscall.Getpgrp())
	if errors.Is(err, ErrOpenFilesUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(f.Name())
	for _, path := range paths {
		if resolved, _ := filepath.EvalSymlinks(path); resolved == want {
			return
		}
	}
	t.Fatalf("GroupOpenFiles = %q, want it to include %q", paths, want)
}
