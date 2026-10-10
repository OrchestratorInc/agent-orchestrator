//go:build windows

package proxyhost

import (
	"errors"
	"os/exec"

	"golang.org/x/sys/windows"
)

func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &windows.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP, HideWindow: true}
}
func processDead(pid int) (bool, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	defer windows.CloseHandle(h)
	var code uint32
	err = windows.GetExitCodeProcess(h, &code)
	return code != 259, err
}
