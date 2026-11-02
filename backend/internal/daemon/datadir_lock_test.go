package daemon

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/datadirlock"
)

// A daemon that finds its data dir owned logs that it is waiting, then gives
// up after the bound with ErrLocked instead of opening ao.db.
func TestLockDataDirWaitsThenRefusesToStart(t *testing.T) {
	dir := t.TempDir()
	held, err := datadirlock.Acquire(context.Background(), dir, 0)
	if err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	t.Cleanup(func() { _ = held.Release() })

	var logs bytes.Buffer
	const wait = 200 * time.Millisecond
	start := time.Now()
	_, err = lockDataDir(context.Background(), dir, wait, slog.New(slog.NewTextHandler(&logs, nil)))
	if !errors.Is(err, datadirlock.ErrLocked) {
		t.Fatalf("lockDataDir = %v, want ErrLocked", err)
	}
	if elapsed := time.Since(start); elapsed < wait {
		t.Fatalf("gave up after %s, want at least %s", elapsed, wait)
	}
	if !strings.Contains(err.Error(), "refusing to start") {
		t.Fatalf("error should say the daemon refuses to start: %v", err)
	}
	if !strings.Contains(logs.String(), "waiting for another AO process to release the data dir") {
		t.Fatalf("no waiting log line in:\n%s", logs.String())
	}
}

// Only a lock that is still held gets the refusal verdict; any other failure
// while waiting (here a cancelled context) comes back as itself.
func TestLockDataDirReturnsOtherErrorsAsIs(t *testing.T) {
	dir := t.TempDir()
	held, err := datadirlock.Acquire(context.Background(), dir, 0)
	if err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	t.Cleanup(func() { _ = held.Release() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = lockDataDir(ctx, dir, time.Minute, slog.New(slog.DiscardHandler))
	if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "refusing to start") {
		t.Fatalf("lockDataDir = %v, want context.Canceled without the lock verdict", err)
	}
}
