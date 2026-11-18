package reaper

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type signalLCM struct{ seen chan domain.SessionID }

func (l signalLCM) ApplyRuntimeObservation(_ context.Context, id domain.SessionID, _ ports.RuntimeFacts) error {
	select {
	case l.seen <- id:
	default:
	}
	return nil
}

func TestStart_WaitsForStartAfter(t *testing.T) {
	start := make(chan struct{})
	lcm := signalLCM{seen: make(chan domain.SessionID, 1)}
	r := New(lcm, fakeSessions{rows: []domain.SessionRecord{probableSession("s1")}}, fakeRuntime{alive: true},
		Config{Tick: time.Millisecond, Logger: quietLogger(), StartAfter: start})
	loopCtx, cancel := context.WithCancel(context.Background())
	done := r.Start(loopCtx)
	defer func() { cancel(); <-done }()

	select {
	case <-lcm.seen:
		t.Fatal("the loop ticked before StartAfter closed")
	case <-time.After(50 * time.Millisecond):
	}
	close(start)
	select {
	case <-lcm.seen:
	case <-time.After(5 * time.Second):
		t.Fatal("the loop did not tick after StartAfter closed")
	}
}
