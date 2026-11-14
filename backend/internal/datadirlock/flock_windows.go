//go:build windows

package datadirlock

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

const lockOffset = 1 << 30

func tryLock(file *os.File) error {
	ol := &windows.Overlapped{Offset: lockOffset}
	err := windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		ol,
	)
	return lockError(err)
}

func lockError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING):
		return ErrLocked
	case errors.Is(err, windows.ERROR_NOT_SUPPORTED) || errors.Is(err, windows.ERROR_INVALID_FUNCTION):
		return fmt.Errorf("%w: %w", ErrUnsupported, err)
	}
	return err
}

func unlock(file *os.File) error {
	ol := &windows.Overlapped{Offset: lockOffset}
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, ol)
}
