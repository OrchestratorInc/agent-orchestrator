package sessionmanager

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeKimiLock(t *testing.T, configDir, nativeID string, pid int, hostname string, startedAt int64) {
	t.Helper()
	dir := filepath.Join(configDir, "locks", "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"version":1,"token":"t","pid":%d,"hostname":%q,"startedAt":%d,"clientIdentity":"kimi_code_cli"}`, pid, hostname, startedAt)
	if err := os.WriteFile(filepath.Join(dir, nativeID+".lock"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestKimiSessionLocked(t *testing.T) {
	const nativeID = "session_11111111-2222-3333-4444-555555555555"
	host, _ := os.Hostname()
	now := time.Now().UnixMilli()
	// A pid far above the OS maximum is never a running process.
	const deadPID = 2147483646

	t.Run("no lock file", func(t *testing.T) {
		if kimiSessionLocked(context.Background(), t.TempDir(), nativeID) {
			t.Fatal("no lock file must read as not open elsewhere")
		}
	})

	t.Run("malformed lock", func(t *testing.T) {
		dir := t.TempDir()
		lockDir := filepath.Join(dir, "locks", "sessions")
		if err := os.MkdirAll(lockDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(lockDir, nativeID+".lock"), []byte("{not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		if kimiSessionLocked(context.Background(), dir, nativeID) {
			t.Fatal("a malformed lock must not count as held")
		}
	})

	t.Run("dead pid on this host", func(t *testing.T) {
		dir := t.TempDir()
		writeKimiLock(t, dir, nativeID, deadPID, host, now)
		if kimiSessionLocked(context.Background(), dir, nativeID) {
			t.Fatal("a lock whose pid is gone must not count as held")
		}
	})

	t.Run("lock held on another host", func(t *testing.T) {
		dir := t.TempDir()
		// A dead pid, but a different hostname means AO cannot verify it and must
		// treat the session as occupied rather than adopt it underneath a writer.
		writeKimiLock(t, dir, nativeID, deadPID, "some-other-machine", now)
		if !kimiSessionLocked(context.Background(), dir, nativeID) {
			t.Fatal("a lock from another host must count as held")
		}
	})

	t.Run("live pid on this host", func(t *testing.T) {
		dir := t.TempDir()
		// This test process is alive and started before the lock's timestamp.
		writeKimiLock(t, dir, nativeID, os.Getpid(), host, now)
		if !kimiSessionLocked(context.Background(), dir, nativeID) {
			t.Fatal("a lock held by a live process must count as held")
		}
	})
}
