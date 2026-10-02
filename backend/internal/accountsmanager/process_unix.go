//go:build !windows

package accountsmanager

import (
	"os/exec"
	"syscall"
)

func configureRunnerProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
