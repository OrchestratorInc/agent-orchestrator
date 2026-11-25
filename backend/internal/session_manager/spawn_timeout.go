package sessionmanager

import (
	"context"
	"errors"
)

// SpawnTimeoutError is a spawn stage failure caused by the spawn's budget
// running out. A git or agent process killed by an expired context reports
// "signal: killed" or "peer disconnected" rather than
// context.DeadlineExceeded; this keeps the stage error for the message and
// the logs while classifying the failure as the timeout it was.
type SpawnTimeoutError struct{ Err error }

func (e *SpawnTimeoutError) Error() string { return "timed out: " + e.Err.Error() }

func (e *SpawnTimeoutError) Unwrap() []error { return []error{context.DeadlineExceeded, e.Err} }

// RelabelSpawnTimeout marks err as a SpawnTimeoutError when any of ctxs ran
// out its deadline, whatever the stage returned. Nil and already-marked errors
// pass through unchanged.
func RelabelSpawnTimeout(err error, ctxs ...context.Context) error {
	var marked *SpawnTimeoutError
	if err == nil || errors.As(err, &marked) {
		return err
	}
	for _, ctx := range ctxs {
		if ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return &SpawnTimeoutError{Err: err}
		}
	}
	return err
}
