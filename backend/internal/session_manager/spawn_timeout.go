package sessionmanager

import (
	"context"
	"errors"
)

type SpawnTimeoutError struct{ Err error }

func (e *SpawnTimeoutError) Error() string { return "timed out: " + e.Err.Error() }

func (e *SpawnTimeoutError) Unwrap() []error { return []error{context.DeadlineExceeded, e.Err} }

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
