//go:build windows

package processalive

import (
	"context"
	"errors"

	"golang.org/x/sys/windows"
)

func processGone(_ context.Context, pid int) bool {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return errors.Is(err, windows.ERROR_INVALID_PARAMETER) || errors.Is(err, windows.ERROR_NOT_FOUND)
	}
	defer windows.CloseHandle(handle)
	status, err := windows.WaitForSingleObject(handle, 0)
	return err == nil && status == windows.WAIT_OBJECT_0
}
