//go:build !windows

package previewserver

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

func previewCommand(name string, args ...string) *exec.Cmd {
	cmd := aoprocess.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return cmd
}

// errStalePreviewPID reports that a recorded preview PID could not be
// re-verified as the process AO launched, so no signal was sent.
var errStalePreviewPID = errors.New("preview pid is stale or recycled; refusing to signal")

// bootTicksPrefix marks a start time read as the boot's id plus clock ticks
// since that boot.
const bootTicksPrefix = "boot+"

// bootID names the running Linux boot. Ticks since boot repeat from one boot to
// the next, and the registry outlives a reboot: without the id, a record left
// by a daemon that died before the reboot could match an unrelated process that
// started at the same tick. It is read on every call, so one failed read
// doesn't stick for the daemon's lifetime.
func bootID() (string, error) {
	id, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	return strings.TrimSpace(string(id)), err
}

// previewProcessStartTime returns the kernel-reported start time of pid as an
// opaque string, or "" when it cannot be read (the process is gone, or ps
// failed). It is captured at launch and compared verbatim before any group
// signal: a bare PID stops identifying anything the moment the child is
// reaped, and a group kill on a recycled number destroys an unrelated process
// family — a tmux server is its own group leader, so one stale group kill took
// out every tmux session at once (issue #3475).
//
// On Linux it is the boot id plus the start in clock ticks since boot
// (/proc/<pid>/stat field 22). ps derives lstart from the boot time, which
// moves whenever the wall clock steps (a VM resumed from a pause, an NTP
// step): a preview launched before the step stopped matching its own record,
// and AO refused to stop it. A Linux host whose boot id can't be read falls
// back to lstart, as before boot ticks.
func previewProcessStartTime(pid int) string {
	if pid <= 0 {
		return ""
	}
	if runtime.GOOS == "linux" {
		if boot, err := bootID(); err == nil {
			return bootTicksStartTime(boot, pid)
		}
	}
	return psStartTime(pid)
}

// bootTicksStartTime formats pid's start as boot+<boot>+<ticks since boot>, or
// returns "" when /proc has no well-formed entry for pid.
func bootTicksStartTime(boot string, pid int) string {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return ""
	}
	// The command name sits in parentheses and may itself contain ") ". The
	// fields after it start at field 3, so field 22 is fields[19].
	end := bytes.LastIndexByte(stat, ')')
	if end < 0 {
		return ""
	}
	fields := strings.Fields(string(stat[end+1:]))
	if len(fields) <= 19 {
		return ""
	}
	if _, err := strconv.ParseUint(fields[19], 10, 64); err != nil {
		return ""
	}
	return bootTicksPrefix + boot + "+" + fields[19]
}

// psStartTime is ps's lstart: the wall-clock start, formatted in the daemon's
// time zone and locale. On macOS the kernel keeps that start, so a clock step
// doesn't move it, but a time zone or locale change does. It is also what AO
// recorded on Linux before boot ticks.
func psStartTime(pid int) string {
	out, err := aoprocess.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// previewGroupHasMembers reports whether any process still belongs to process
// group pid.
func previewGroupHasMembers(pid int) bool {
	return aoprocess.Command("pgrep", "-g", strconv.Itoa(pid)).Run() == nil
}

// signalPreviewGroup delivers sig to pid's process group only while the group
// leader is still alive and its kernel start time matches the recorded one
// verbatim. Anything less is not proof that the group is AO's preview: once
// the leader is gone the number may already have been recycled, and a group
// kill on a recycled PID can take down an unrelated family (a tmux server and
// every agent session in it, issue #3475). A descendant that survives its
// leader is deliberately leaked rather than guessed at.
//
// ponytail: the verify-then-kill pair is not atomic; without pidfd-style
// primitives a recycle in that microsecond window is untestable and accepted.
func signalPreviewGroup(pid int, recordedStart string, sig syscall.Signal) error {
	if pid <= 0 {
		return nil
	}
	if recordedStart == "" {
		return errStalePreviewPID
	}
	var current string
	if strings.HasPrefix(recordedStart, bootTicksPrefix) {
		boot, err := bootID()
		if err != nil {
			return fmt.Errorf("%w: %w", errStalePreviewPID, err)
		}
		current = bootTicksStartTime(boot, pid)
	} else {
		current = psStartTime(pid) // macOS, or a Linux record from before boot ticks
	}
	if current == "" {
		return nil // leader gone: nothing provably AO's remains
	}
	if current != recordedStart {
		return errStalePreviewPID
	}
	err := syscall.Kill(-pid, sig)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

// signalPreviewRoot signals only the direct child through os.Process, which
// refuses to signal an already-reaped child, so it can never hit a recycled
// PID. It is the fallback for runs whose launch identity could not be
// captured: descendants may leak, innocents never die.
func signalPreviewRoot(cmd *exec.Cmd, sig syscall.Signal) error {
	err := cmd.Process.Signal(sig)
	if err == nil || errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func terminatePreviewProcess(cmd *exec.Cmd, startTime string) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if startTime == "" {
		return signalPreviewRoot(cmd, syscall.SIGTERM)
	}
	return signalPreviewGroup(cmd.Process.Pid, startTime, syscall.SIGTERM)
}

func forceKillPreviewProcess(cmd *exec.Cmd, startTime string) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if startTime == "" {
		return signalPreviewRoot(cmd, syscall.SIGKILL)
	}
	return signalPreviewGroup(cmd.Process.Pid, startTime, syscall.SIGKILL)
}

func forceKillPreviewPID(pid int, startTime string) error {
	return signalPreviewGroup(pid, startTime, syscall.SIGKILL)
}
