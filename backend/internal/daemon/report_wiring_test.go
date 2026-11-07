package daemon

import (
	"encoding/json"
	"testing"

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
