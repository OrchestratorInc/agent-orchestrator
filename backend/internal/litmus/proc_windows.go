//go:build windows

package litmus

import (
	"os"
	"os/exec"
)

// Launching targets is not supported on Windows yet; these keep the package compiling.
func ownGroup(*exec.Cmd) {}

func sigkill(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}

func sigkillGroup(pgid int) { sigkill(pgid) }
