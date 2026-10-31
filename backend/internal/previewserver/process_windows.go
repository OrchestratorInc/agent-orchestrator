//go:build windows

package previewserver

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"golang.org/x/sys/windows"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

func previewCommand(name string, args ...string) *exec.Cmd {
	// aoprocess starts batch shims' targets directly, so argv never goes through
	// cmd.exe quoting (issue #7212).
	cmd := aoprocess.Command(name, args...)
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP
	return cmd
}

// errStalePreviewPID reports that a recorded preview PID could not be
// re-verified as the process AO launched, so nothing was killed.
var errStalePreviewPID = errors.New("preview pid is stale or recycled; refusing to signal")

// previewProcessStartTime returns pid's kernel creation time as an opaque
// decimal string, or "" when the process does not exist or cannot be queried.
// It is captured at launch and compared verbatim before any tree kill: a bare
// PID stops identifying anything once the child is reaped, and Windows
// recycles PIDs the same way, so `taskkill /T` on a recycled number would
// kill an unrelated process tree (issue #3475).
func previewProcessStartTime(pid int) string {
	if pid <= 0 {
		return ""
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return ""
	}
	defer func() { _ = windows.CloseHandle(handle) }()
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return ""
	}
	return strconv.FormatUint(uint64(creation.HighDateTime)<<32|uint64(creation.LowDateTime), 10)
}

// killPreviewTree taskkills pid's tree only after re-verifying that the
// number still identifies the process AO launched. A missing root is a no-op:
// /T walks the tree from the root, so with the root gone there is nothing
// left that the daemon can still attribute to itself.
func killPreviewTree(pid int, recordedStart string, force bool) error {
	if pid <= 0 {
		return nil
	}
	if recordedStart == "" {
		return errStalePreviewPID
	}
	current := previewProcessStartTime(pid)
	if current == "" {
		return nil
	}
	if current != recordedStart {
		return errStalePreviewPID
	}
	args := []string{"/PID", strconv.Itoa(pid), "/T"}
	if force {
		args = append(args, "/F")
	}
	return aoprocess.Command("taskkill", args...).Run()
}

// killPreviewRoot terminates only the direct child through os.Process, which
// refuses to act on an already-reaped child, so it can never hit a recycled
// PID. It is the fallback for runs whose launch identity could not be
// captured: descendants may leak, innocents never die.
func killPreviewRoot(cmd *exec.Cmd) error {
	err := cmd.Process.Kill()
	if err == nil || errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

func terminatePreviewProcess(cmd *exec.Cmd, startTime string) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if startTime == "" {
		return killPreviewRoot(cmd)
	}
	return killPreviewTree(cmd.Process.Pid, startTime, false)
}

func forceKillPreviewPID(pid int, startTime string) error {
	return killPreviewTree(pid, startTime, true)
}

func forceKillPreviewProcess(cmd *exec.Cmd, startTime string) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if startTime == "" {
		return killPreviewRoot(cmd)
	}
	return killPreviewTree(cmd.Process.Pid, startTime, true)
}
