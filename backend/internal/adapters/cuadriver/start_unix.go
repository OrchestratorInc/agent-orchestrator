//go:build !windows

package cuadriver

import (
	"fmt"
	"os/exec"
	"syscall"
)

// startDetached starts a long-lived Cua Driver process in its own session so
// it outlives the AO daemon, and reaps it if it exits first.
func startDetached(binary string, args ...string) error {
	cmd := exec.Command(binary, args...) //nolint:forbidigo // Cua Driver's service must outlive the daemon, like the detached chat host.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start cua-driver serve: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
