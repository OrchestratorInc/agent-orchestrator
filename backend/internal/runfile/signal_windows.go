//go:build windows

package runfile

import "os"

func signalDaemon(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	defer func() { _ = process.Release() }()
	return process.Kill()
}
