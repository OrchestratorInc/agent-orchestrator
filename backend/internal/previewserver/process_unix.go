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

var errStalePreviewPID = errors.New("preview pid is stale or recycled; refusing to signal")

const bootTicksPrefix = "boot+"

func bootID() (string, error) {
	id, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	return strings.TrimSpace(string(id)), err
}

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

func bootTicksStartTime(boot string, pid int) string {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return ""
	}
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

func psStartTime(pid int) string {
	out, err := aoprocess.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func previewGroupHasMembers(pid int) bool {
	return aoprocess.Command("pgrep", "-g", strconv.Itoa(pid)).Run() == nil
}

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
		current = psStartTime(pid)
	}
	if current == "" {
		return nil
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
