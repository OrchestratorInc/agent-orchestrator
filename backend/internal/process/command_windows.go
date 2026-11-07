//go:build windows

package process

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// configureHidden suppresses the transient console window. It preserves any
// SysProcAttr already on the command — rewriteBatchShim may have put the
// cmd.exe launch line there — and only adds the hide flags.
func configureHidden(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW
	cmd.SysProcAttr.HideWindow = true
}
