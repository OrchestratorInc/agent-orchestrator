//go:build windows

package previewserver

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

func previewCommand(name string, args ...string) *exec.Cmd {
	if resolved, err := exec.LookPath(name); err == nil && isWindowsBatchFile(resolved) {
		shell := strings.TrimSpace(os.Getenv("COMSPEC"))
		if shell == "" {
			shell = "cmd.exe"
		}
		cmd := aoprocess.Command(shell) //nolint:gosec // COMSPEC is the OS-selected command interpreter for .cmd/.bat shims
		cmd.Args = nil
		if cmd.SysProcAttr == nil {
			cmd.SysProcAttr = &syscall.SysProcAttr{}
		}
		cmd.SysProcAttr.CmdLine = `/d /s /c "` + windowsBatchCommandLine(resolved, args) + `"`
		cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP
		return cmd
	}
	cmd := aoprocess.Command(name, args...)
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP
	return cmd
}

func isWindowsBatchFile(path string) bool {
	extension := filepath.Ext(path)
	return strings.EqualFold(extension, ".cmd") || strings.EqualFold(extension, ".bat")
}

func windowsBatchCommandLine(executable string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, quoteWindowsBatchArg(executable))
	for _, arg := range args {
		parts = append(parts, quoteWindowsBatchArg(arg))
	}
	return strings.Join(parts, " ")
}

func quoteWindowsBatchArg(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

var errStalePreviewPID = errors.New("preview pid is stale or recycled; refusing to signal")

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
