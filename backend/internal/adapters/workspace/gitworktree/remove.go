package gitworktree

import (
	"context"
	"errors"
	"os"
	"time"
)

var errRemoveRetryExhausted = errors.New("worktree removal retry exhausted")

type removeRetryExhaustedError struct {
	err error
}

func (e removeRetryExhaustedError) Error() string { return e.err.Error() }

func (e removeRetryExhaustedError) Unwrap() []error {
	return []error{errRemoveRetryExhausted, e.err}
}

var (
	removeAllAttempts   = 18
	removeAllBackoff    = 50 * time.Millisecond
	removeAllBackoffCap = 500 * time.Millisecond
	removeAllRetryable  = isRetryableRemoveError
	removeAll           = os.RemoveAll
)

func removeAllWithRetry(ctx context.Context, path string) error {
	err := removeAll(path)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if isPermissionRemoveError(err) && repairRemovePermissions(path) {
		if err = removeAll(path); err == nil || errors.Is(err, os.ErrNotExist) {
			return nil
		}
	}
	if !removeAllRetryable(err) {
		return err
	}

	timer := time.NewTimer(0)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()

	backoff := removeAllBackoff
	for range removeAllAttempts - 1 {
		timer.Reset(backoff)
		select {
		case <-ctx.Done():
			return err
		case <-timer.C:
		}
		if backoff *= 2; backoff > removeAllBackoffCap {
			backoff = removeAllBackoffCap
		}

		if err = removeAll(path); err == nil || errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if !removeAllRetryable(err) {
			return err
		}
	}
	return removeRetryExhaustedError{err: err}
}
