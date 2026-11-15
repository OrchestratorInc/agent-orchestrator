//go:build unix

package datadirlock

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func tryLock(file *os.File) error {
	return lockError(unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB))
}

func lockError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN):
		return ErrLocked
	case errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.ENOLCK):
		return fmt.Errorf("%w: %w", ErrUnsupported, err)
	}
	return err
}

func unlock(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
