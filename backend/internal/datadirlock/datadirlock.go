// Package datadirlock guards a daemon's SQLite data directory with an exclusive,
// OS-level advisory file lock.
//
// The lock is scoped to the data directory itself, independent of AO_PORT and
// AO_RUN_FILE, so two daemons pointed at the same AO_DATA_DIR with different
// ports/run files can never both own the database. Without it a second daemon
// can open and hot-migrate SQLite underneath a still-running first daemon,
// invalidating the first daemon's prepared statements against the new schema
// (issue #3716).
//
// Crash recovery is free: the lock lives on the open file descriptor, so the
// kernel releases it automatically when the holder exits — cleanly or by crash,
// SIGKILL included. A successor therefore reclaims a stale lock with no
// PID-liveness bookkeeping. os.OpenFile opens close-on-exec, so tmux servers and
// chat hosts that deliberately outlive the daemon never inherit the lock.
package datadirlock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// lockFileName is the advisory-lock file created inside the data directory. It
// carries the holding process's PID as plain text purely for diagnostics; the
// lock itself is enforced by the OS, not by the file's contents.
const lockFileName = "daemon.lock"

// retryInterval paces Acquire while another process holds the lock. Neither
// flock nor LockFileEx can block with a timeout, so a short poll is the
// portable way to bound the wait.
const retryInterval = 100 * time.Millisecond

// ErrLocked is returned by Acquire when another live process still holds the
// data-directory lock after the wait.
var ErrLocked = errors.New("data dir is in use by another AO process")

// ErrUnsupported is returned by Acquire when the data dir's filesystem cannot
// lock files at all (some NFS, SMB and FUSE mounts). Callers start without the
// guard rather than refuse to run, which is how AO behaved before the lock.
var ErrUnsupported = errors.New("file locks are not supported on this filesystem")

// Lock is a held data-directory lock. Release (or the process exiting) frees it.
type Lock struct {
	file *os.File
}

// Acquire takes the exclusive lock for dataDir, creating the directory if
// needed. While another process holds the lock it retries until wait elapses
// (wait <= 0 tries once), then returns an error wrapping ErrLocked that names
// the holder's PID when it can be read.
func Acquire(ctx context.Context, dataDir string, wait time.Duration) (*Lock, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, fmt.Errorf("data dir lock: data dir is required")
	}
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, fmt.Errorf("data dir lock: create %s: %w", dataDir, err)
	}
	path := filepath.Join(dataDir, lockFileName)
	// O_RDWR (not O_TRUNC): the PID line is rewritten only after the lock is
	// held, so a failed acquire leaves the current owner's PID readable.
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("data dir lock: open %s: %w", path, err)
	}
	deadline := time.Now().Add(wait)
	err = tryLock(file)
	for errors.Is(err, ErrLocked) && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, ctx.Err()
		case <-time.After(retryInterval):
		}
		err = tryLock(file)
	}
	if err != nil {
		holder := readHolderPID(file)
		_ = file.Close()
		if !errors.Is(err, ErrLocked) {
			return nil, fmt.Errorf("data dir lock: %s: %w", path, err)
		}
		if holder > 0 {
			return nil, fmt.Errorf("%w: %s (pid %d)", ErrLocked, dataDir, holder)
		}
		return nil, fmt.Errorf("%w: %s", ErrLocked, dataDir)
	}
	// The lock is ours: record our PID for anyone who later inspects the file.
	if err := writeHolderPID(file, os.Getpid()); err != nil {
		_ = unlock(file)
		_ = file.Close()
		return nil, fmt.Errorf("data dir lock: record owner: %w", err)
	}
	return &Lock{file: file}, nil
}

// Release frees the lock. It is safe to call once; a nil or already-released
// Lock is a no-op. Closing the file descriptor drops the OS lock.
func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := unlock(l.file)
	closeErr := l.file.Close()
	l.file = nil
	if unlockErr != nil {
		return fmt.Errorf("data dir lock: release: %w", unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("data dir lock: close: %w", closeErr)
	}
	return nil
}

// writeHolderPID records pid for diagnostics. No fsync: readers see the page
// cache, and the lock itself lives in the kernel, not in the file.
func writeHolderPID(file *os.File, pid int) error {
	if err := file.Truncate(0); err != nil {
		return err
	}
	_, err := file.WriteAt([]byte(strconv.Itoa(pid)+"\n"), 0)
	return err
}

// readHolderPID best-effort reads the PID the current owner recorded. Any error
// (empty/partial file during a race) yields 0, which callers treat as unknown.
func readHolderPID(file *os.File) int {
	buf := make([]byte, 32)
	n, err := file.ReadAt(buf, 0)
	if n == 0 && err != nil {
		return 0
	}
	pid, convErr := strconv.Atoi(strings.TrimSpace(string(buf[:n])))
	if convErr != nil {
		return 0
	}
	return pid
}
