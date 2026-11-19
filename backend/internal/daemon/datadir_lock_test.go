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
	"github.com/aoagents/agent-orchestrator/backend/internal/service/systeminstall"
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

func TestDataDirTakeoverWaitIncludesInstallerDrain(t *testing.T) {
	for _, tc := range []struct {
		name     string
		shutdown time.Duration
		want     time.Duration
	}{
		{"default", 10 * time.Second, 382 * time.Second},
		{"short shutdown", time.Second, 310 * time.Second},
		{"long shutdown", 5 * time.Minute, 2718 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := dataDirTakeoverWait(tc.shutdown, systeminstall.DefaultShutdownTimeout())
			if got != tc.want {
				t.Fatalf("takeover wait = %s, want %s", got, tc.want)
			}
		})
	}
}

// A replacement must keep waiting when installer cleanup holds the old
// daemon's lock past the previous takeover deadline. Scale seconds to 5ms.
func TestLockDataDirWaitsForInstallerCleanup(t *testing.T) {
	const scale = 5 * time.Millisecond
	wait := dataDirTakeoverWait(10*time.Second, systeminstall.DefaultShutdownTimeout()) / time.Second * scale
	dir := t.TempDir()
	held, err := datadirlock.Acquire(context.Background(), dir, 0)
	if err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	t.Cleanup(func() { _ = held.Release() })
	released := make(chan error, 1)
	go func() {
		time.Sleep(120 * scale)
		released <- held.Release()
	}()
	// Always join the predecessor before removing the temporary data dir.
	t.Cleanup(func() {
		if err := <-released; err != nil {
			t.Errorf("release predecessor lock: %v", err)
		}
	})
	lock, err := lockDataDir(context.Background(), dir, wait, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("replacement refused during installer cleanup: %v", err)
	}
	t.Cleanup(func() { _ = lock.Release() })
}
