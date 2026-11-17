package process

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkingDirOfSelf(t *testing.T) {
	got, err := WorkingDir(context.Background(), os.Getpid())
	if errors.Is(err, ErrWorkingDirUnsupported) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	gotReal, _ := filepath.EvalSymlinks(got)
	wantReal, _ := filepath.EvalSymlinks(want)
	if gotReal != wantReal {
		t.Fatalf("WorkingDir = %q, want %q", got, want)
	}
}
