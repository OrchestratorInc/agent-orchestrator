//go:build !windows

package previewserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
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
// macOS uses the same native start identity as Chat; old lstart records still
// use the legacy reader when checked.
func previewProcessStartTime(pid int) string {
	if pid <= 0 {
		return ""
	}
	if runtime.GOOS == "linux" {
		if start, err := processalive.BootStartTime(context.Background(), pid); err == nil {
			return start
		}
	}
	if runtime.GOOS == "darwin" {
		return processalive.Identify(context.Background(), pid).StartID
	}
	return processalive.StartTime(context.Background(), pid)
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
		var err error
		current, err = processalive.BootStartTime(context.Background(), pid)
		if err != nil {
			return fmt.Errorf("%w: %w", errStalePreviewPID, err)
		}
	} else if strings.HasPrefix(recordedStart, "darwin:") {
		current = processalive.Identify(context.Background(), pid).StartID
		if current == "" && processalive.Alive(pid) {
			return errStalePreviewPID
		}
	} else {
		current = processalive.StartTime(context.Background(), pid)
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
