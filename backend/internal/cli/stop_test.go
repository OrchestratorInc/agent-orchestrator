package cli

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
)

// TestWaitForStoppedKeepsRunFileFromConcurrentStart guards against deleting a
// fresh daemon's handshake: if a concurrent `ao start` replaces running.json
// with a new live PID while we are polling the PID we stopped, waitForStopped
// must report stopped but leave the new run-file intact.
func TestWaitForStoppedKeepsRunFileFromConcurrentStart(t *testing.T) {
	dir := t.TempDir()
	runFile := filepath.Join(dir, "running.json")

	const stoppedPID, newPID = 1111, 2222
	// running.json now belongs to a different, live daemon.
	if err := runfile.Write(runFile, runfile.Info{PID: newPID, Port: 3001, StartedAt: time.Unix(100, 0).UTC()}); err != nil {
		t.Fatal(err)
	}

	c := &commandContext{deps: Deps{
		ProcessAlive: func(pid int) bool { return pid == newPID }, // stoppedPID is dead
		Now:          func() time.Time { return time.Unix(200, 0).UTC() },
		Sleep:        func(time.Duration) {},
	}.withDefaults()}

	st, err := c.waitForStopped(context.Background(), stoppedPID, runFile, dir, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != stateStopped {
		t.Fatalf("state = %q, want stopped", st.State)
	}

	info, err := runfile.Read(runFile)
	if err != nil {
		t.Fatal(err)
	}
	if info == nil {
		t.Fatal("new daemon's run-file was deleted by stop of a different PID")
		return
	}
	if info.PID != newPID {
		t.Fatalf("run-file PID = %d, want %d (new daemon)", info.PID, newPID)
	}
}

// TestWaitForStoppedRemovesOwnRunFile confirms the normal path still cleans up:
// when the dead PID owns the run-file, it is removed.
func TestWaitForStoppedRemovesOwnRunFile(t *testing.T) {
	dir := t.TempDir()
	runFile := filepath.Join(dir, "running.json")

	const stoppedPID = 1111
	if err := runfile.Write(runFile, runfile.Info{PID: stoppedPID, Port: 3001, StartedAt: time.Unix(100, 0).UTC()}); err != nil {
		t.Fatal(err)
	}

	c := &commandContext{deps: Deps{
		ProcessAlive: func(int) bool { return false },
		Now:          func() time.Time { return time.Unix(200, 0).UTC() },
		Sleep:        func(time.Duration) {},
	}.withDefaults()}

	st, err := c.waitForStopped(context.Background(), stoppedPID, runFile, dir, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != stateStopped {
		t.Fatalf("state = %q, want stopped", st.State)
	}
	info, err := runfile.Read(runFile)
	if err != nil {
		t.Fatal(err)
	}
	if info != nil {
		t.Fatalf("own run-file should have been removed, got %#v", info)
	}
}

func TestWaitForStoppedWaitsAfterRunFileRemovedUntilProcessExits(t *testing.T) {
	dir := t.TempDir()
	runFile := filepath.Join(dir, "running.json")

	const stoppedPID = 1111
	now := time.Unix(200, 0).UTC()
	aliveChecks := 0
	sleeps := 0
	c := &commandContext{deps: Deps{
		ProcessAlive: func(int) bool {
			aliveChecks++
			return aliveChecks < 3
		},
		Now: func() time.Time {
			return now
		},
		Sleep: func(d time.Duration) {
			sleeps++
			now = now.Add(d)
		},
	}.withDefaults()}

	st, err := c.waitForStopped(context.Background(), stoppedPID, runFile, dir, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != stateStopped {
		t.Fatalf("state = %q, want stopped", st.State)
	}
	if sleeps == 0 {
		t.Fatal("waitForStopped returned before waiting for process exit")
	}
	if aliveChecks < 3 {
		t.Fatalf("process checks = %d, want at least 3", aliveChecks)
	}
}

// TestWaitForStoppedFailsWhenRunFileGoneButProcessLingers prevents ao stop
// from reporting success merely because the daemon removed running.json. A
// daemon that remains alive after the requested stop timeout is an actionable
// failed stop: it can become an orphan after its desktop parent exits.
func TestWaitForStoppedFailsWhenRunFileGoneButProcessLingers(t *testing.T) {
	dir := t.TempDir()
	runFile := filepath.Join(dir, "running.json") // never written: run-file already gone

	const stoppedPID = 1111
	now := time.Unix(200, 0).UTC()
	c := &commandContext{deps: Deps{
		ProcessAlive: func(int) bool { return true }, // process never exits
		Now:          func() time.Time { return now },
		Sleep:        func(d time.Duration) { now = now.Add(d) },
	}.withDefaults()}

	_, err := c.waitForStopped(context.Background(), stoppedPID, runFile, dir, time.Second)
	if err == nil {
		t.Fatal("waitForStopped returned success while the daemon process remained alive")
	}
	if got, want := err.Error(), "daemon pid 1111 removed run-file but did not exit within 1s"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}
