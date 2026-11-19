package session

import (
	"context"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// CardSummaryInterval is the shortest gap between two card summaries of one
// session. The final summary is never held back.
const CardSummaryInterval = time.Minute

// CardSummaryStore writes the card summary of a live session.
type CardSummaryStore interface {
	UpdateSessionCardSummary(ctx context.Context, id domain.SessionID, summary string) (bool, error)
	FillSessionCardSummary(ctx context.Context, id domain.SessionID, summary, previous string) (bool, error)
}

// CardSummaryLimiter writes agent card summaries at most once per
// CardSummaryInterval per session, and nothing while card summaries are off.
// Each `ao summary` reaches it twice, from the event stream when the command
// starts and from the CLI call itself, so a repeat of the last accepted text is
// the same update, not a new one.
type CardSummaryLimiter struct {
	store   CardSummaryStore
	enabled func(context.Context) bool
	now     func() time.Time

	mu   sync.Mutex
	last map[domain.SessionID]cardSummaryWrite
}

type cardSummaryWrite struct {
	text string
	at   time.Time
}

// NewCardSummaryLimiter returns a limiter that writes through store while
// enabled reports true.
func NewCardSummaryLimiter(store CardSummaryStore, enabled func(context.Context) bool, now func() time.Time) *CardSummaryLimiter {
	return &CardSummaryLimiter{store: store, enabled: enabled, now: now, last: map[domain.SessionID]cardSummaryWrite{}}
}

// Update stores summary unless the session's previous summary is younger than
// CardSummaryInterval and done is false. retryAfter is how long until the next
// update is accepted when this one was held back. found is false when no live
// session has id. While card summaries are off it writes nothing and reports
// success, so a worker started while they were on carries on quietly.
func (l *CardSummaryLimiter) Update(ctx context.Context, id domain.SessionID, summary string, done bool) (found bool, retryAfter time.Duration, err error) {
	if !l.enabled(ctx) {
		return true, 0, nil
	}
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

// FillSessionCardSummary gives a card the text of a done report when the agent
// never set a summary itself (the card is empty or still shows previous, an
// earlier fill). It writes nothing while card summaries are off.
func (l *CardSummaryLimiter) FillSessionCardSummary(ctx context.Context, id domain.SessionID, summary, previous string) (bool, error) {
	if !l.enabled(ctx) {
		return false, nil
	}
	return l.store.FillSessionCardSummary(ctx, id, summary, previous)
}
