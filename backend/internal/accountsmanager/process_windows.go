//go:build windows

package accountsmanager

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

func configureRunnerProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
}
