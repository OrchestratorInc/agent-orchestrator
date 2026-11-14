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

const lockFileName = "daemon.lock"

const retryInterval = 100 * time.Millisecond

var ErrLocked = errors.New("data dir is in use by another Orchestrator process")

var ErrUnsupported = errors.New("file locks are not supported on this filesystem")

type Lock struct {
	file *os.File
}

func Acquire(ctx context.Context, dataDir string, wait time.Duration) (*Lock, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, fmt.Errorf("data dir lock: data dir is required")
	}
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, fmt.Errorf("data dir lock: create %s: %w", dataDir, err)
	}
	path := filepath.Join(dataDir, lockFileName)
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
	if err := writeHolderPID(file, os.Getpid()); err != nil {
		_ = unlock(file)
		_ = file.Close()
		return nil, fmt.Errorf("data dir lock: record owner: %w", err)
	}
	return &Lock{file: file}, nil
}

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

func writeHolderPID(file *os.File, pid int) error {
	if err := file.Truncate(0); err != nil {
		return err
	}
	_, err := file.WriteAt([]byte(strconv.Itoa(pid)+"\n"), 0)
	return err
}

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
