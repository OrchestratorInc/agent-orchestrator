//go:build windows

package acp

import (
	"os/exec"
	"strconv"
	"syscall"

	"golang.org/x/sys/windows"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

func configureProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW | windows.CREATE_NEW_PROCESS_GROUP
	cmd.SysProcAttr.HideWindow = true
}

func killProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	// Node launches the provider as a child. taskkill /T is the Windows equivalent
	// of killing the Unix process group and avoids leaving Claude behind.
	kill := aoprocess.Command("taskkill", "/PID", strconv.Itoa(cmd.Process.Pid), "/T", "/F")
	if err := kill.Run(); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}
