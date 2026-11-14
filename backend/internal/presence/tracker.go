package presence

import (
	"sort"
	"sync"
	"time"
)

const TTL = 20 * time.Second

const Retention = 7 * 24 * time.Hour

const maxTracked = 512

type Tracker struct {
	mu   sync.RWMutex
	seen map[string]time.Time

	Now func() time.Time
}

func NewTracker() *Tracker {
	return &Tracker{seen: map[string]time.Time{}, Now: time.Now}
}

func (t *Tracker) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

func (t *Tracker) Touch(installID string) {
	if installID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.seen[installID] = t.now()
	t.pruneLocked()
}

func (t *Tracker) Live() map[string]bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	cutoff := t.now().Add(-TTL)
	live := make(map[string]bool, len(t.seen))
	for id, at := range t.seen {
		if !at.Before(cutoff) {
			live[id] = true
		}
	}
	return live
}

func (t *Tracker) LastSeen(installID string) (time.Time, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	at, ok := t.seen[installID]
	return at, ok
}

func (t *Tracker) pruneLocked() {
	cutoff := t.now().Add(-Retention)
	for id, at := range t.seen {
		if at.Before(cutoff) {
			delete(t.seen, id)
		}
	}
	if len(t.seen) <= maxTracked {
		return
	}
	type entry struct {
		id string
		at time.Time
	}
	entries := make([]entry, 0, len(t.seen))
	for id, at := range t.seen {
		entries = append(entries, entry{id, at})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].at.Before(entries[j].at) })
	for _, e := range entries[:len(entries)-maxTracked] {
		delete(t.seen, e.id)
	}
}

func (t *Tracker) Size() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.seen)
}
