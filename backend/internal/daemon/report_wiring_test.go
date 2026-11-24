package daemon

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type reportActivityRecorder struct {
	states     []domain.ActivityState
	terminated []bool
}

func (r *reportActivityRecorder) SessionActivityChanged(state domain.ActivityState, terminated bool) {
	r.states = append(r.states, state)
	r.terminated = append(r.terminated, terminated)
}

type startupReportCoordinator struct {
	reportActivityRecorder
	broadcaster *cdc.Broadcaster
}

func (c *startupReportCoordinator) Start(context.Context) <-chan struct{} {
	// Model a worker stopping after the first scan while Start is returning.
	c.broadcaster.Publish(cdc.Event{Type: cdc.EventSessionUpdated, Payload: json.RawMessage(`{"id":"w","activity":"idle"}`)})
	return nil
}

func TestStartReportCoordinatorForwardsActivityDuringStartup(t *testing.T) {
	broadcaster := cdc.NewBroadcaster()
	coordinator := &startupReportCoordinator{broadcaster: broadcaster}
	_, unsubscribe := startReportCoordinator(t.Context(), broadcaster, coordinator)
	defer unsubscribe()
	if len(coordinator.states) != 1 || coordinator.states[0] != domain.ActivityIdle {
		t.Fatalf("relayed startup activity = %v, want [idle]", coordinator.states)
	}
}

func TestForwardReportActivityRelaysSessionUpdates(t *testing.T) {
	recorder := &reportActivityRecorder{}
	forward := forwardReportActivity(recorder)
	forward(cdc.Event{Type: cdc.EventSessionUpdated, Payload: json.RawMessage(`{"id":"w","activity":"idle","isTerminated":false}`)})
	forward(cdc.Event{Type: cdc.EventSessionUpdated, Payload: json.RawMessage(`{"id":"w","activity":"active","isTerminated":true}`)})
	forward(cdc.Event{Type: cdc.EventSessionCreated, Payload: json.RawMessage(`{"id":"w","activity":"idle"}`)})
	forward(cdc.Event{Type: cdc.EventSessionUpdated, Payload: json.RawMessage(`not json`)})
	if len(recorder.states) != 2 ||
		recorder.states[0] != domain.ActivityIdle || recorder.terminated[0] ||
		recorder.states[1] != domain.ActivityActive || !recorder.terminated[1] {
		t.Fatalf("relayed states=%v terminated=%v", recorder.states, recorder.terminated)
	}
}

type workerReportRecorder struct {
	transitions []domain.ActivityState
	writes      chan domain.ReportState
}

func (r *workerReportRecorder) Transition(_ domain.SessionID, state domain.ActivityState, _ bool) (domain.ReportState, time.Time) {
	r.transitions = append(r.transitions, state)
	if state == domain.ActivityIdle {
		return domain.ReportDone, time.Time{}
	}
	return "", time.Time{}
}

func (r *workerReportRecorder) Write(_ context.Context, _ domain.SessionID, state domain.ReportState, _ time.Time) {
	r.writes <- state
}

func TestForwardWorkerReportsWritesChosenReport(t *testing.T) {
	recorder := &workerReportRecorder{writes: make(chan domain.ReportState, 4)}
	forward := forwardWorkerReports(t.Context(), recorder)
	forward(cdc.Event{Type: cdc.EventSessionUpdated, SessionID: "w", Payload: json.RawMessage(`{"id":"w","activity":"active"}`)})
	forward(cdc.Event{Type: cdc.EventSessionCreated, SessionID: "w", Payload: json.RawMessage(`{"id":"w","activity":"idle"}`)})
	forward(cdc.Event{Type: cdc.EventSessionUpdated, Payload: json.RawMessage(`not json`)})
	forward(cdc.Event{Type: cdc.EventSessionUpdated, Payload: json.RawMessage(`{"id":"w","activity":"idle"}`)})
	if len(recorder.transitions) != 2 {
		t.Fatalf("transitions = %v, want [active idle]", recorder.transitions)
	}
	select {
	case state := <-recorder.writes:
		if state != domain.ReportDone {
			t.Fatalf("wrote %q, want done", state)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no report written")
	}
}
