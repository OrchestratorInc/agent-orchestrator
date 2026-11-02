//go:build unix

package datadirlock

import (
	"errors"
	"testing"

	"golang.org/x/sys/unix"
)

// A held lock and a file system that cannot lock are different verdicts: the
// first makes the daemon wait, the second makes it start without the guard.
func TestLockErrorSeparatesHeldFromUnsupported(t *testing.T) {
	if err := lockError(unix.EWOULDBLOCK); !errors.Is(err, ErrLocked) {
		t.Fatalf("lockError(EWOULDBLOCK) = %v, want ErrLocked", err)
	}
	for _, errno := range []unix.Errno{unix.ENOTSUP, unix.EOPNOTSUPP, unix.ENOLCK} {
		err := lockError(errno)
		if !errors.Is(err, ErrUnsupported) || !errors.Is(err, errno) || errors.Is(err, ErrLocked) {
			t.Fatalf("lockError(%v) = %v, want ErrUnsupported wrapping it", errno, err)
		}
	}
	if err := lockError(unix.EIO); errors.Is(err, ErrLocked) || errors.Is(err, ErrUnsupported) {
		t.Fatalf("lockError(EIO) = %v, want it returned as is", err)
	}
}
