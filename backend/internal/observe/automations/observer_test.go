package automations

import (
	"context"
	"testing"
	"time"
)

type fakePoller struct {
	times []time.Time
	err   error
}

func (f *fakePoller) Tick(_ context.Context, at time.Time) error {
	f.times = append(f.times, at)
	return f.err
}

// The observer owns cadence only; schedule decisions stay in the service and
// receive one normalized timestamp per poll.
func TestPollDelegatesUTCNowToService(t *testing.T) {
	local := time.Date(2026, time.August, 25, 18, 30, 0, 0, time.FixedZone("IST", 5*60*60+30*60))
	poller := &fakePoller{}
	observer := New(poller, Config{Clock: func() time.Time { return local }})
	if err := observer.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(poller.times) != 1 || poller.times[0].Location() != time.UTC || !poller.times[0].Equal(local) {
		t.Fatalf("poll times = %#v, want one UTC instant %s", poller.times, local.UTC())
	}
}

type signalingPoller struct{ polls chan struct{} }

func (s signalingPoller) Tick(context.Context, time.Time) error {
	s.polls <- struct{}{}
	return nil
}

// A wake must poll right away instead of waiting a whole tick; an archived
// session's review would otherwise start up to fifteen seconds late.
func TestWakePollsBeforeTheNextTick(t *testing.T) {
	poller := signalingPoller{polls: make(chan struct{}, 4)}
	observer := New(poller, Config{Tick: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	done := observer.Start(ctx)
	t.Cleanup(func() { cancel(); <-done })
	<-poller.polls // initial poll
	observer.Wake()
	select {
	case <-poller.polls:
	case <-time.After(5 * time.Second):
		t.Fatal("Wake did not poll before the hourly tick")
	}
}
