//go:build !windows

package agentauth

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func tryLockFile(f *os.File) (release func(), busy bool, err error) {
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return nil, errors.Is(err, unix.EWOULDBLOCK), err
	}
	return func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN) }, false, nil
}
