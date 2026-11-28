//go:build windows

package agentauth

import (
	"errors"
	"math"
	"os"

	"golang.org/x/sys/windows"
)

// tryLockFile takes Crush's exclusive whole-file LockFileEx lock on f
// without blocking. busy reports that another process holds it.
func tryLockFile(f *os.File) (release func(), busy bool, err error) {
	h := windows.Handle(f.Fd())
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY)
	if err := windows.LockFileEx(h, flags, 0, math.MaxUint32, math.MaxUint32, new(windows.Overlapped)); err != nil {
		return nil, errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING), err
	}
	return func() { _ = windows.UnlockFileEx(h, 0, math.MaxUint32, math.MaxUint32, new(windows.Overlapped)) }, false, nil
}
