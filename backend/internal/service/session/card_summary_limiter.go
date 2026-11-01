package session

import (
	"context"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// CardSummaryInterval is the shortest gap between two card summaries of one
// session. The final summary is never held back.
const CardSummaryInterval = 3 * time.Minute

// CardSummaryStore writes the card summary of a live session.
type CardSummaryStore interface {
	UpdateSessionCardSummary(ctx context.Context, id domain.SessionID, summary string) (bool, error)
}

// CardSummaryLimiter writes agent card summaries at most once per
// CardSummaryInterval per session. Each `ao summary` reaches it twice, from the
// event stream when the command starts and from the CLI call itself, so a
// repeat of the last accepted text is the same update, not a new one.
type CardSummaryLimiter struct {
	store CardSummaryStore
	now   func() time.Time

	mu   sync.Mutex
	last map[domain.SessionID]cardSummaryWrite
}

type cardSummaryWrite struct {
	text string
	at   time.Time
}

// NewCardSummaryLimiter returns a limiter that writes through store.
func NewCardSummaryLimiter(store CardSummaryStore, now func() time.Time) *CardSummaryLimiter {
	return &CardSummaryLimiter{store: store, now: now, last: map[domain.SessionID]cardSummaryWrite{}}
}

// Update stores summary unless the session's previous summary is younger than
// CardSummaryInterval and done is false. retryAfter is how long until the next
// update is accepted when this one was held back. found is false when no live
// session has id.
func (l *CardSummaryLimiter) Update(ctx context.Context, id domain.SessionID, summary string, done bool) (found bool, retryAfter time.Duration, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	prev, seen := l.last[id]
	if seen && prev.text == summary {
		return true, 0, nil
	}
	if seen && !done {
		if wait := CardSummaryInterval - now.Sub(prev.at); wait > 0 {
			return true, wait, nil
		}
	}
	found, err = l.store.UpdateSessionCardSummary(ctx, id, summary)
	if err != nil || !found {
		return found, 0, err
	}
	l.last[id] = cardSummaryWrite{text: summary, at: now}
	return true, 0, nil
}
