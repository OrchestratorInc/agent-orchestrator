package main

import (
	"context"
	"sync/atomic"
	"time"
)

// prefetchMaxAge bounds how old a prefetched lookup may be when it is used.
// Startup consumes it within seconds; a checkout that failed and waits for the
// user to reopen the session falls back to a fresh fetch instead.
const prefetchMaxAge = 30 * time.Second

// prefetch runs one control-plane lookup in the background, beside the
// checkout, and hands the result to the first caller that asks for it. Every
// later caller, and the first one too if the lookup failed or aged past maxAge,
// fetches for itself. It never changes what a caller sees, only when the
// request was made: startup no longer waits a round trip for lookups that do
// not depend on the workspace.
type prefetch[T any] struct {
	done    chan struct{}
	value   T
	err     error
	at      time.Time
	maxAge  time.Duration
	claimed atomic.Bool
}

func startPrefetch[T any](
	ctx context.Context,
	maxAge time.Duration,
	fetch func(context.Context) (T, error),
) *prefetch[T] {
	p := &prefetch[T]{done: make(chan struct{}), maxAge: maxAge}
	go func() {
		defer close(p.done)
		p.value, p.err = fetch(ctx)
		p.at = time.Now()
	}()
	return p
}

// take returns the prefetched value to its single consumer. ok is false when
// the caller must fetch for itself: a nil prefetch, a second caller, a failed
// or stale lookup, or a caller whose context ended first.
func (p *prefetch[T]) take(ctx context.Context) (T, bool) {
	var zero T
	if p == nil || !p.claimed.CompareAndSwap(false, true) {
		return zero, false
	}
	select {
	case <-p.done:
	case <-ctx.Done():
		return zero, false
	}
	if p.err != nil || time.Since(p.at) > p.maxAge {
		return zero, false
	}
	value := p.value
	// Only the claimer reaches here, so clearing races with no one. It keeps
	// a credential from lingering in the prefetch after its single use.
	p.value = zero
	return value, true
}
