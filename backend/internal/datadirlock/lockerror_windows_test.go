//go:build windows

package datadirlock

import (
	"errors"
	"testing"

	"golang.org/x/sys/windows"
)

// A held lock and a file system that cannot lock are different verdicts: the
// first makes the daemon wait, the second makes it start without the guard.
func TestLockErrorSeparatesHeldFromUnsupported(t *testing.T) {
	if err := lockError(windows.ERROR_LOCK_VIOLATION); !errors.Is(err, ErrLocked) {
		t.Fatalf("lockError(ERROR_LOCK_VIOLATION) = %v, want ErrLocked", err)
	}
	for _, errno := range []windows.Errno{windows.ERROR_NOT_SUPPORTED, windows.ERROR_INVALID_FUNCTION} {
		err := lockError(errno)
		if !errors.Is(err, ErrUnsupported) || !errors.Is(err, errno) || errors.Is(err, ErrLocked) {
			t.Fatalf("lockError(%v) = %v, want ErrUnsupported wrapping it", errno, err)
		}
	}
	if err := lockError(windows.ERROR_ACCESS_DENIED); errors.Is(err, ErrLocked) || errors.Is(err, ErrUnsupported) {
		t.Fatalf("lockError(ERROR_ACCESS_DENIED) = %v, want it returned as is", err)
	}
}
