package cdc

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type memorySource struct {
	mu     sync.Mutex
	events []Event
	reads  int
	readAt []time.Time
	fail   int
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
	if m.fail > 0 {
		m.fail--
		return nil, errors.New("read failed")
	}
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
	synctest.Test(t, func(t *testing.T) {
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
		synctest.Wait()
		wake <- struct{}{}
		synctest.Wait()
		if n := len(reads()); n != 1 {
			t.Fatalf("reads before the interval passed = %d, want 1", n)
		}
		time.Sleep(50 * time.Millisecond)
		synctest.Wait()
		got := reads()
		if len(got) != 2 || got[1].Sub(got[0]) != 50*time.Millisecond {
			t.Fatalf("reads = %v, want a second read 50ms after the first", got)
		}
	})
}

func TestPollerWithWakeCoalescesSignalsDuringTheWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := &memorySource{}
		wake := make(chan struct{}, 1)
		p := NewPoller(src, NewBroadcaster(), PollerConfig{Interval: 100 * time.Millisecond, Wake: wake})
		p.fallback = time.Hour
		ctx, cancel := context.WithCancel(context.Background())
		done := p.Start(ctx)
		defer func() { cancel(); <-done }()

		wake <- struct{}{}
		synctest.Wait()
		for range 2 {
			time.Sleep(10 * time.Millisecond)
			wake <- struct{}{}
			synctest.Wait()
		}
		time.Sleep(time.Second)
		synctest.Wait()
		src.mu.Lock()
		reads := src.reads
		src.mu.Unlock()
		if reads != 2 {
			t.Fatalf("reads = %d, want 2: the second poll covers both signals sent during its wait", reads)
		}
	})
}

func TestPollerWithWakeRetriesAFailedReadOfAConsumedSignal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		src := &memorySource{}
		wake := make(chan struct{}, 1)
		p := NewPoller(src, NewBroadcaster(), PollerConfig{Interval: 100 * time.Millisecond, Wake: wake, Logger: slog.New(slog.DiscardHandler)})
		p.fallback = time.Hour
		ctx, cancel := context.WithCancel(context.Background())
		done := p.Start(ctx)
		defer func() { cancel(); <-done }()

		wake <- struct{}{}
		synctest.Wait()
		for range 2 {
			time.Sleep(10 * time.Millisecond)
			wake <- struct{}{}
			synctest.Wait()
		}
		src.mu.Lock()
		src.fail = 1
		src.mu.Unlock()
		time.Sleep(time.Second)
		synctest.Wait()
		src.mu.Lock()
		reads := src.reads
		src.mu.Unlock()
		if reads != 3 {
			t.Fatalf("reads = %d, want 3: the failed read retries once for the signal it consumed", reads)
		}
	})
}
