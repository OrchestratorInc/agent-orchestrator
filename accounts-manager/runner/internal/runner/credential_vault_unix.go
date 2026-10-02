//go:build !windows

package runner

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func validateVaultFile(file *os.File, directory bool) error {
	info, err := file.Stat()
	if err != nil || info.IsDir() != directory || info.Mode().Perm()&0o077 != 0 {
		return errCredentialStorage
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int64(stat.Uid) != int64(os.Geteuid()) || (!directory && (!info.Mode().IsRegular() || stat.Nlink != 1)) {
		return errCredentialStorage
	}
	return nil
}

func protectVaultFile(file *os.File) error {
	return file.Chmod(0o600)
}

func lockVaultFile(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}

func syncVaultDirectory(root *os.Root) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	syncErr := dir.Sync()
	return errors.Join(syncErr, dir.Close())
}
