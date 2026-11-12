// Package wakeguard limits how often AO restarts an exited agent on its own
// because a message arrived for it.
//
// A send to an exited agent restarts it. Without a limit, a project whose
// workers all report at once, or an agent that crashes on every start, would
// restart the agent over and over. The budget keeps a failed restart from being
// retried until a cooldown passes, and caps successful restarts per window so a
// crash loop stops instead of spinning.
package wakeguard

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	// RetryAfterFailure is how long a failed automatic restart blocks the next one.
	RetryAfterFailure = time.Minute
	// Window is the span over which MaxPerWindow counts restarts.
	Window = 10 * time.Minute
	// MaxPerWindow caps automatic restarts of one session within Window.
	MaxPerWindow = 3
)

// ErrRecentFailure wraps the last failure while its cooldown runs.
var ErrRecentFailure = errors.New("the last automatic restart failed moments ago")

// ErrTooManyRestarts reports a session restarted automatically too often.
var ErrTooManyRestarts = errors.New("the agent exited again soon after each automatic restart, so Orchestrator stopped restarting it for a few minutes")

type entry struct {
	starts   []time.Time
	failedAt time.Time
	failure  error
}

// Budget is safe for concurrent use. The zero value is not; use New.
type Budget struct {
	mu         sync.Mutex
	now        func() time.Time
	retryAfter time.Duration
	window     time.Duration
	max        int
	sessions   map[domain.SessionID]*entry
}

// New returns a budget with the package defaults. now may be nil.
func New(now func() time.Time) *Budget {
	return NewWithLimits(now, RetryAfterFailure, Window, MaxPerWindow)
}

// NewWithLimits is New with explicit limits, for tests.
func NewWithLimits(now func() time.Time, retryAfter, window time.Duration, maxStarts int) *Budget {
	if now == nil {
		now = time.Now
	}
	return &Budget{now: now, retryAfter: retryAfter, window: window, max: maxStarts, sessions: map[domain.SessionID]*entry{}}
}

// Admit reserves one automatic restart of id, or says why it may not start.
// Callers deduplicate concurrent senders first, so one in-flight restart
// costs one admission however many messages are waiting on it.
func (b *Budget) Admit(id domain.SessionID) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	e := b.sessions[id]
	if e == nil {
		e = &entry{}
		b.sessions[id] = e
	}
	if e.failure != nil && now.Sub(e.failedAt) < b.retryAfter {
		return fmt.Errorf("%w: %w", ErrRecentFailure, e.failure)
	}
	kept := e.starts[:0]
	for _, at := range e.starts {
		if now.Sub(at) < b.window {
			kept = append(kept, at)
		}
	}
	e.starts = kept
	if b.max > 0 && len(e.starts) >= b.max {
		return ErrTooManyRestarts
	}
	e.starts = append(e.starts, now)
	return nil
}

// Record notes how an admitted restart ended. A success clears the cooldown.
func (b *Budget) Record(id domain.SessionID, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	e := b.sessions[id]
	if e == nil {
		e = &entry{}
		b.sessions[id] = e
	}
	if err == nil {
		e.failure, e.failedAt = nil, time.Time{}
		return
	}
	e.failure, e.failedAt = err, b.now()
}
