//go:build !windows

package previewserver

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeStartTime is a launch identity that can never match a live process.
const fakeStartTime = "Thu Jan  1 00:00:00 1970"

func startSleeper(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()
	cmd := previewCommand(args[0], args[1:]...)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %v: %v", args, err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
	})
	return cmd
}

// sleeperRunning reports whether the un-reaped child is still alive (WNOHANG:
// 0 means running, pid means it exited and became reapable).
func sleeperRunning(t *testing.T, pid int) bool {
	t.Helper()
	var status syscall.WaitStatus
	n, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
	if err != nil {
		t.Fatalf("wait4 %d: %v", pid, err)
	}
	return n == 0
}

// Regression for issue #3475 (A1): a PID recorded by a previous daemon
// generation is stale by construction. If the live process holding that number
// does not carry the recorded launch identity, reap must not kill it — the
// recycled number can belong to anything, including a tmux server whose group
// kill wipes every session at once.
func TestReapPersistedProcessesRefusesMismatchedIdentity(t *testing.T) {
	cmd := startSleeper(t, "sleep", "60")

	dataDir := t.TempDir()
	registry, err := json.Marshal([]persistedProcess{{
		SessionID: "ao-1",
		PID:       cmd.Process.Pid,
		Port:      4173,
		StartedAt: time.Now().UTC(),
		StartTime: fakeStartTime,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, processRegistryFile), registry, 0o600); err != nil {
		t.Fatal(err)
	}

	New(slog.New(slog.NewTextHandler(io.Discard, nil)), dataDir)

	time.Sleep(100 * time.Millisecond)
	if !sleeperRunning(t, cmd.Process.Pid) {
		t.Fatal("reap killed a live process whose identity did not match the recorded preview")
	}
}

// A legacy registry entry (written before launch identities were recorded)
// carries no proof of ownership, so reap must skip it entirely.
func TestReapPersistedProcessesSkipsLegacyEntriesWithoutStartTime(t *testing.T) {
	cmd := startSleeper(t, "sleep", "60")

	dataDir := t.TempDir()
	registry, err := json.Marshal([]persistedProcess{{
		SessionID: "ao-1",
		PID:       cmd.Process.Pid,
		Port:      4173,
		StartedAt: time.Now().UTC(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, processRegistryFile), registry, 0o600); err != nil {
		t.Fatal(err)
	}

	New(slog.New(slog.NewTextHandler(io.Discard, nil)), dataDir)

	time.Sleep(100 * time.Millisecond)
	if !sleeperRunning(t, cmd.Process.Pid) {
		t.Fatal("reap killed a live process from a legacy registry entry with no recorded identity")
	}
}

// Reap still does its job when the recorded identity checks out.
func TestReapPersistedProcessesKillsVerifiedProcess(t *testing.T) {
	cmd := startSleeper(t, "sleep", "60")
	startTime := previewProcessStartTime(cmd.Process.Pid)
	if startTime == "" {
		t.Fatal("could not capture launch identity")
	}

	dataDir := t.TempDir()
	registry, err := json.Marshal([]persistedProcess{{
		SessionID: "ao-1",
		PID:       cmd.Process.Pid,
		Port:      4173,
		StartedAt: time.Now().UTC(),
		StartTime: startTime,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, processRegistryFile), registry, 0o600); err != nil {
		t.Fatal(err)
	}

	New(slog.New(slog.NewTextHandler(io.Discard, nil)), dataDir)

	waitUntil(t, 2*time.Second, func() bool { return !sleeperRunning(t, cmd.Process.Pid) },
		"verified orphaned preview process was not killed")
}

func TestSignalPreviewGroupRefusesMismatchedStartTime(t *testing.T) {
	cmd := startSleeper(t, "sleep", "60")

	err := signalPreviewGroup(cmd.Process.Pid, fakeStartTime, syscall.SIGKILL)
	if !errors.Is(err, errStalePreviewPID) {
		t.Fatalf("signalPreviewGroup err = %v, want errStalePreviewPID", err)
	}
	time.Sleep(100 * time.Millisecond)
	if !sleeperRunning(t, cmd.Process.Pid) {
		t.Fatal("group kill went through despite a mismatched launch identity")
	}
}

func TestSignalPreviewGroupKillsVerifiedGroup(t *testing.T) {
	cmd := startSleeper(t, "sleep", "60")
	startTime := previewProcessStartTime(cmd.Process.Pid)
	if startTime == "" {
		t.Fatal("could not capture launch identity")
	}

	if err := signalPreviewGroup(cmd.Process.Pid, startTime, syscall.SIGKILL); err != nil {
		t.Fatalf("signalPreviewGroup: %v", err)
	}
	waitUntil(t, 2*time.Second, func() bool { return !sleeperRunning(t, cmd.Process.Pid) },
		"verified preview group was not killed")
}

// Once the root is reaped, the PID no longer provably belongs to AO's
// preview, so no group kill may happen even though descendants survive: a
// leaked preview process is safer than a group kill landing on a recycled
// PID. The orphan must still be alive after the escalation call.
func TestForceKillLeaksOrphanedGroupAfterRootIsReaped(t *testing.T) {
	cmd := previewCommand("/bin/sh", "-c", "sleep 60 & sleep 1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
	startTime := previewProcessStartTime(pid)
	if startTime == "" {
		t.Fatal("could not capture launch identity")
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("root exit: %v", err)
	}
	if !previewGroupHasMembers(pid) {
		t.Fatal("expected an orphaned group member after the root was reaped")
	}

	if err := forceKillPreviewProcess(cmd, startTime); err != nil {
		t.Fatalf("forceKillPreviewProcess: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if !previewGroupHasMembers(pid) {
		t.Fatal("post-exit escalation killed the orphaned group; it must leak rather than signal an unverifiable PID")
	}
}

func waitUntil(t *testing.T, timeout time.Duration, done func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal(message)
}

// requireBootTicks returns pid's start time and its ticks, failing unless the
// start time is boot+<this boot's id>+<non-zero ticks since boot>.
func requireBootTicks(t *testing.T, pid int) (start, ticks string) {
	t.Helper()
	boot, err := bootID()
	if err != nil {
		t.Fatalf("read boot id: %v", err)
	}
	start = previewProcessStartTime(pid)
	ticks, ok := strings.CutPrefix(start, bootTicksPrefix+boot+"+")
	if n, err := strconv.ParseUint(ticks, 10, 64); !ok || err != nil || n == 0 {
		t.Fatalf("start time = %q, want %s%s+<ticks since boot>", start, bootTicksPrefix, boot)
	}
	return start, ticks
}

// On Linux the launch identity is the boot id plus clock ticks since boot,
// which a wall-clock step (a VM resumed from a pause) does not move; ps's
// lstart moves with it.
func TestPreviewProcessStartTimeUsesBootTicksOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("boot ticks come from /proc")
	}
	cmd := startSleeper(t, "sleep", "60")
	requireBootTicks(t, cmd.Process.Pid)
}

// The #3475 case under boot ticks: a recorded PID that now belongs to a later
// process in the same boot must not match the record.
func TestSignalPreviewGroupRefusesLaterProcessFromSameBoot(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("boot ticks come from /proc")
	}
	earlier := startSleeper(t, "sleep", "60")
	recorded, _ := requireBootTicks(t, earlier.Process.Pid)
	time.Sleep(50 * time.Millisecond) // several clock ticks (USER_HZ is 100)
	later := startSleeper(t, "sleep", "60")
	if err := signalPreviewGroup(later.Process.Pid, recorded, syscall.SIGKILL); !errors.Is(err, errStalePreviewPID) {
		t.Fatalf("signalPreviewGroup = %v, want errStalePreviewPID", err)
	}
	time.Sleep(100 * time.Millisecond)
	if !sleeperRunning(t, later.Process.Pid) {
		t.Fatal("an earlier process's record killed a later process in the same boot")
	}
}

// The registry outlives a reboot, and ticks since boot repeat across boots: a
// record from another boot must not match a process that started at the same
// tick in this one.
func TestSignalPreviewGroupRefusesRecordFromAnotherBoot(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("boot ticks come from /proc")
	}
	cmd := startSleeper(t, "sleep", "60")
	_, ticks := requireBootTicks(t, cmd.Process.Pid)
	otherBoot := bootTicksPrefix + "00000000-0000-0000-0000-000000000000+" + ticks
	if err := signalPreviewGroup(cmd.Process.Pid, otherBoot, syscall.SIGKILL); !errors.Is(err, errStalePreviewPID) {
		t.Fatalf("signalPreviewGroup = %v, want errStalePreviewPID", err)
	}
	time.Sleep(100 * time.Millisecond)
	if !sleeperRunning(t, cmd.Process.Pid) {
		t.Fatal("a record from another boot killed a live process")
	}
}

// A preview recorded with ps's lstart (macOS, or Linux before boot ticks)
// can still be stopped.
func TestSignalPreviewGroupAcceptsLstartRecord(t *testing.T) {
	cmd := startSleeper(t, "sleep", "60")
	recorded := psStartTime(cmd.Process.Pid)
	if recorded == "" {
		t.Fatal("ps reported no start time")
	}
	if err := signalPreviewGroup(cmd.Process.Pid, recorded, syscall.SIGKILL); err != nil {
		t.Fatalf("signalPreviewGroup: %v", err)
	}
	waitUntil(t, 2*time.Second, func() bool { return !sleeperRunning(t, cmd.Process.Pid) },
		"preview recorded with lstart was not killed")
}
