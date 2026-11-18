package cdc

import (
	"context"
	"sync"
	"testing"
	"time"
)

type memorySource struct {
	mu     sync.Mutex
	events []Event
	reads  int
	readAt []time.Time
}

func (m *memorySource) add(seqs ...int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, seq := range seqs {
		m.events = append(m.events, Event{Seq: seq})
	}
}

func (m *memorySource) EventsAfter(_ context.Context, after int64, limit int) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reads++
	m.readAt = append(m.readAt, time.Now())
	var out []Event
	for _, e := range m.events {
		if e.Seq > after && len(out) < limit {
			out = append(out, e)
		}
	}
	return out, nil
}

func (m *memorySource) LatestSeq(context.Context) (int64, error) { return 0, nil }

func TestPollerWithWakePollsOnSignalAndDrainsFullBatches(t *testing.T) {
	src := &memorySource{}
	bc := NewBroadcaster()
	got := make(chan int64, 10)
	bc.Subscribe(func(e Event) { got <- e.Seq })
	wake := make(chan struct{}, 1)
	p := NewPoller(src, bc, PollerConfig{Interval: time.Millisecond, Batch: 2, Wake: wake})
	p.fallback = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := p.Start(ctx)
	defer func() { cancel(); <-done }()

	src.add(1, 2, 3)
	time.Sleep(50 * time.Millisecond)
	src.mu.Lock()
	reads := src.reads
	src.mu.Unlock()
	if reads != 0 {
		t.Fatalf("polled %d times without a signal", reads)
	}

	wake <- struct{}{}
	for want := int64(1); want <= 3; want++ {
		select {
		case seq := <-got:
			if seq != want {
				t.Fatalf("event seq %d, want %d", seq, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("event %d not delivered after one signal", want)
		}
	}
}

func TestPollerWithWakeSpacesPollsOnBackToBackSignals(t *testing.T) {
	src := &memorySource{}
	wake := make(chan struct{}, 1)
	p := NewPoller(src, NewBroadcaster(), PollerConfig{Interval: 50 * time.Millisecond, Wake: wake})
	p.fallback = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	done := p.Start(ctx)
	defer func() { cancel(); <-done }()

	reads := func() []time.Time {
		src.mu.Lock()
		defer src.mu.Unlock()
		return append([]time.Time(nil), src.readAt...)
	}
	wake <- struct{}{}
	deadline := time.Now().Add(5 * time.Second)
	for len(reads()) < 1 {
		if time.Now().After(deadline) {
			t.Fatal("first signal did not poll")
		}
		time.Sleep(time.Millisecond)
	}
	wake <- struct{}{}
	for len(reads()) < 2 {
		if time.Now().After(deadline) {
			t.Fatal("second signal did not poll")
		}
		time.Sleep(time.Millisecond)
	}
	if gap := reads()[1].Sub(reads()[0]); gap < 50*time.Millisecond {
		t.Fatalf("polls %v apart, want at least the 50ms interval", gap)
	}
}
