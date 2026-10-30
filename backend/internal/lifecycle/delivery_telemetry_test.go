package lifecycle

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/deliverytelemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// deliveryStore adds the waiting-input counter and the recorder's read surface
// to the lifecycle fake.
type deliveryStore struct {
	*fakeStore
	waits   map[domain.SessionID]int64
	claimMu sync.Mutex
	claimed map[domain.SessionID]bool
}

func (d *deliveryStore) IncrementSessionWaitingInputCount(_ context.Context, id domain.SessionID) error {
	d.waits[id]++
	return nil
}
func (d *deliveryStore) GetSessionTelemetry(_ context.Context, id domain.SessionID) (domain.SessionTelemetryFacts, error) {
	return domain.SessionTelemetryFacts{CreatedBy: domain.SessionCreatedByUser, WaitingInputCount: d.waits[id]}, nil
}
func (*deliveryStore) ListReportsBySession(context.Context, domain.SessionID) ([]domain.ReportRecord, error) {
	return nil, nil
}
func (*deliveryStore) UsageProjectID(context.Context, string, string) (string, error) {
	return "", nil
}
func (d *deliveryStore) ClaimSessionEndTelemetry(_ context.Context, id domain.SessionID) (bool, error) {
	d.claimMu.Lock()
	defer d.claimMu.Unlock()
	if d.claimed == nil {
		d.claimed = map[domain.SessionID]bool{}
	}
	if d.claimed[id] {
		return false, nil
	}
	d.claimed[id] = true
	return true, nil
}

func newDeliveryManager() (*Manager, *deliveryStore, *telemetrySink) {
	st := &deliveryStore{fakeStore: newFakeStore(), waits: map[domain.SessionID]int64{}}
	sink := &telemetrySink{}
	m := New(st, nil, WithTelemetry(sink), WithDeliveryTelemetry(deliverytelemetry.New(sink, st)))
	return m, st, sink
}

func endedEvents(sink *telemetrySink) []ports.TelemetryEvent {
	var out []ports.TelemetryEvent
	for _, ev := range sink.events {
		if ev.Name == "ao.sessions.ended" {
			out = append(out, ev)
		}
	}
	return out
}

func TestWaitingInputEntriesAreCountedForSessionEnded(t *testing.T) {
	m, st, sink := newDeliveryManager()
	now := time.Unix(100, 0).UTC()
	m.clock = func() time.Time { return now }
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}}

	for _, state := range []domain.ActivityState{domain.ActivityWaitingInput, domain.ActivityActive, domain.ActivityWaitingInput, domain.ActivityBlocked, domain.ActivityActive} {
		now = now.Add(time.Second)
		if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{Valid: true, State: state, Timestamp: now}); err != nil {
			t.Fatal(err)
		}
	}
	// waiting -> blocked stays inside the needs-input family: two pauses, not three.
	if got := st.waits["mer-1"]; got != 2 {
		t.Fatalf("waiting_input_count = %d, want 2 (events %#v)", got, sink.events)
	}
}

func TestRuntimeDeathReportsFailedSessionOnce(t *testing.T) {
	m, st, sink := newDeliveryManager()
	rec := working("mer-1")
	rec.Kind = domain.KindWorker
	rec.Harness = domain.HarnessCodex
	rec.CreatedAt = time.Now().Add(-time.Hour)
	rec.Activity.LastActivityAt = time.Now().Add(-2 * time.Minute)
	st.sessions["mer-1"] = rec
	st.waits["mer-1"] = 1

	for i := 0; i < 2; i++ {
		if err := m.ApplyRuntimeObservation(ctx, "mer-1", ports.RuntimeFacts{Runtime: ports.ProbeDead, Workload: ports.ProbeFailed}); err != nil {
			t.Fatal(err)
		}
	}
	ended := endedEvents(sink)
	if len(ended) != 1 {
		t.Fatalf("ended events = %d, want 1", len(ended))
	}
	p := ended[0].Payload
	if p["outcome"] != "failed" || p["harness"] != "codex" || p["waiting_input_count"] != int64(1) || p["created_by"] != "user" {
		t.Fatalf("payload = %#v", p)
	}
}

func TestTrackerCloseReportsCompletedSession(t *testing.T) {
	m, st, sink := newDeliveryManager()
	st.sessions["mer-1"] = working("mer-1")
	o := ports.TrackerObservation{Fetched: true, Issue: ports.TrackerIssueObservation{State: domain.IssueDone}}
	if err := m.ApplyTrackerFacts(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if err := m.ApplyTrackerFacts(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	ended := endedEvents(sink)
	if len(ended) != 1 || ended[0].Payload["outcome"] != "completed" {
		t.Fatalf("ended = %#v, want one completed", ended)
	}
}
