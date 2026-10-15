//go:build windows

package datadirlock

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockOffset places the one locked byte past the PID text. Windows range locks
// are mandatory, so locking byte 0 would stop a contender from reading the
// holder's PID for its error message. Locking beyond EOF is allowed.
const lockOffset = 1 << 30

// tryLock takes a non-blocking exclusive lock via
// LockFileEx(LOCKFILE_EXCLUSIVE_LOCK|LOCKFILE_FAIL_IMMEDIATELY). ERROR_LOCK_VIOLATION
// means another process holds it, reported as ErrLocked. Windows drops the lock
// when the owning process's handle is closed on exit, so a crashed holder does
// not wedge the data directory.
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
	if err == nil {
		return nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
		return ErrLocked
	}
	return err
}

func unlock(file *os.File) error {
	ol := &windows.Overlapped{Offset: lockOffset}
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, ol)
}
