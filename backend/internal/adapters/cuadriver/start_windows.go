//go:build windows

package cuadriver

import (
	"fmt"
	"os/exec"

	"golang.org/x/sys/windows"
)

// startDetached starts a long-lived Cua Driver process without a console,
// detached from the AO daemon so it keeps serving after the daemon exits.
func startDetached(binary string, args ...string) error {
	cmd := exec.Command(binary, args...) //nolint:forbidigo // Cua Driver's service must outlive the daemon, like the detached chat host.
	cmd.SysProcAttr = &windows.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP, HideWindow: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start cua-driver serve: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
