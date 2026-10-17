package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A template that installs the harness after the worker starts (the Coder
// agent reports ready before its own bootstrap finishes) still gets its agent:
// the worker keeps checking until the binary appears.
func TestWaitForHarnessFindsALateInstall(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	restore := setHarnessWait(t, 10*time.Millisecond, 5*time.Second)
	defer restore()
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n"), 0o755)
	}()
	if err := waitForHarness(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), "claude-code"); err != nil {
		t.Fatalf("waitForHarness = %v, want the late install found", err)
	}
}

// A binary that never appears keeps the existing unavailable error, and an
// unknown harness is reported at once.
func TestWaitForHarnessGivesUp(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	restore := setHarnessWait(t, 10*time.Millisecond, 60*time.Millisecond)
	defer restore()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	started := time.Now()
	if err := waitForHarness(context.Background(), logger, "claude-code"); err == nil {
		t.Fatal("waitForHarness found a missing binary")
	}
	if time.Since(started) < 50*time.Millisecond {
		t.Fatal("waitForHarness gave up before its limit")
	}
	started = time.Now()
	if err := waitForHarness(context.Background(), logger, "no-such-harness"); err == nil || time.Since(started) > 50*time.Millisecond {
		t.Fatalf("unknown harness: err = %v after %s", err, time.Since(started))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForHarness(ctx, logger, "claude-code"); err == nil {
		t.Fatal("waitForHarness ignored a cancelled context")
	}
}

func setHarnessWait(t *testing.T, interval, limit time.Duration) func() {
	t.Helper()
	previousInterval, previousLimit := harnessWaitInterval, harnessWaitLimit
	harnessWaitInterval, harnessWaitLimit = interval, limit
	return func() { harnessWaitInterval, harnessWaitLimit = previousInterval, previousLimit }
}
