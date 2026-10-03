// Package contextpressure holds the newest context-fullness reading each
// session's harness has reported.
//
// The readings are deliberately in-memory only. They describe a live agent
// process, so a reading cannot outlive the daemon that saw it: after a restart
// the truthful answer is "unknown" until the harness reports again, and the
// read model already distinguishes unknown from a reported zero.
package contextpressure

import (
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	// retention bounds how long an unrefreshed reading is kept. A harness
	// reports while it works, so anything older belongs to a session that has
	// stopped reporting — usually one that ended. Readers apply their own,
	// shorter freshness rule; this only stops the map growing without bound in
	// a daemon that outlives thousands of sessions.
	retention = time.Hour
	// pruneThreshold is the size at which a write sweeps expired entries. It
	// keeps the sweep off the common path: a daemon running a normal number of
	// sessions never pays for it.
	pruneThreshold = 128
)

// Store is a concurrent map of session id to its newest reading. The zero value
// is not usable; call NewStore.
type Store struct {
	mu       sync.RWMutex
	readings map[domain.SessionID]domain.ContextPressure
	now      func() time.Time
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{readings: make(map[domain.SessionID]domain.ContextPressure), now: time.Now}
}

// Record stores a reading, clamping the percentage into 0..100 so a harness
// that over-reports its own window cannot publish an impossible figure.
// An out-of-range percent is clamped rather than rejected: the reading is
// still evidence the context is full, which is the case that matters.
func (s *Store) Record(id domain.SessionID, percent int, source string, observedAt time.Time) {
	if id == "" {
		return
	}
	switch {
	case percent < 0:
		percent = 0
	case percent > 100:
		percent = 100
	}
	if observedAt.IsZero() {
		observedAt = s.now()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// Reports can arrive out of order across a reconnect; keep the newest.
	if existing, ok := s.readings[id]; ok && existing.ObservedAt.After(observedAt) {
		return
	}
	s.readings[id] = domain.ContextPressure{
		ContextUsedPercent: percent,
		Source:             source,
		ObservedAt:         observedAt.UTC(),
	}
	if len(s.readings) > pruneThreshold {
		cutoff := s.now().Add(-retention)
		for key, reading := range s.readings {
			if reading.ObservedAt.Before(cutoff) {
				delete(s.readings, key)
			}
		}
	}
}

// Get returns the session's newest reading, or nil when none was reported.
func (s *Store) Get(id domain.SessionID) *domain.ContextPressure {
	s.mu.RLock()
	defer s.mu.RUnlock()
	reading, ok := s.readings[id]
	if !ok {
		return nil
	}
	return &reading
}
