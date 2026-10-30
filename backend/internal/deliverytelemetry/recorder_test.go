package deliverytelemetry

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type captureSink struct {
	mu     sync.Mutex
	events []ports.TelemetryEvent
}

func (c *captureSink) Emit(_ context.Context, ev ports.TelemetryEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, ev)
}

func (c *captureSink) Close(context.Context) error { return nil }

func (c *captureSink) only(t *testing.T, name string) ports.TelemetryEvent {
	t.Helper()
	var found []ports.TelemetryEvent
	for _, ev := range c.events {
		if ev.Name == name {
			found = append(found, ev)
		}
	}
	if len(found) != 1 {
		t.Fatalf("%s events = %d, want 1 (all: %+v)", name, len(found), c.events)
	}
	return found[0]
}

type fakeStore struct {
	facts      map[domain.SessionID]domain.SessionTelemetryFacts
	prs        map[domain.SessionID][]domain.PullRequest
	reports    map[domain.SessionID][]domain.ReportRecord
	projectIDs map[string]string
	projectErr error
	lookups    int
}

func (f *fakeStore) UsageProjectID(_ context.Context, projectID, installID string) (string, error) {
	f.lookups++
	if f.projectErr != nil {
		return "", f.projectErr
	}
	return f.projectIDs[projectID+"|"+installID], nil
}

func (f *fakeStore) GetSessionTelemetry(_ context.Context, id domain.SessionID) (domain.SessionTelemetryFacts, error) {
	return f.facts[id], nil
}

func (f *fakeStore) ListPRsBySession(_ context.Context, id domain.SessionID) ([]domain.PullRequest, error) {
	return f.prs[id], nil
}

func (f *fakeStore) ListReportsBySession(_ context.Context, id domain.SessionID) ([]domain.ReportRecord, error) {
	return f.reports[id], nil
}

var t0 = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

func newRecorder(store *fakeStore) (*Recorder, *captureSink) {
	sink := &captureSink{}
	r := New(sink, store)
	r.now = func() time.Time { return t0 }
	return r, sink
}

func TestSessionOutcome(t *testing.T) {
	cases := []struct {
		name     string
		reason   EndReason
		activity domain.ActivityState
		hadPR    bool
		done     bool
		want     string
	}{
		{"crash wins even with a PR", EndRuntimeDied, domain.ActivityIdle, true, true, OutcomeFailed},
		{"merge teardown", EndPRMerged, domain.ActivityIdle, true, false, OutcomePROpened},
		{"killed after opening a PR", EndKilled, domain.ActivityActive, true, false, OutcomePROpened},
		{"done report without a PR", EndKilled, domain.ActivityIdle, false, true, OutcomeCompleted},
		{"issue closed without a PR", EndIssueClosed, domain.ActivityIdle, false, false, OutcomeCompleted},
		{"killed mid-turn", EndKilled, domain.ActivityActive, false, false, OutcomeKilled},
		{"killed while waiting on the user", EndKilled, domain.ActivityWaitingInput, false, false, OutcomeAbandoned},
		{"killed while idle", EndKilled, domain.ActivityIdle, false, false, OutcomeAbandoned},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sessionOutcome(tc.reason, tc.activity, tc.hadPR, tc.done); got != tc.want {
				t.Fatalf("outcome = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSessionEndedPayload(t *testing.T) {
	rec := domain.SessionRecord{
		ID: "w-1", ProjectID: "proj", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		CreatedAt: t0.Add(-90 * time.Second), Activity: domain.Activity{State: domain.ActivityIdle},
	}
	store := &fakeStore{
		facts:   map[domain.SessionID]domain.SessionTelemetryFacts{"w-1": {CreatedBy: domain.SessionCreatedByOrchestrator, WaitingInputCount: 3}},
		reports: map[domain.SessionID][]domain.ReportRecord{"w-1": {{State: domain.ReportCheckpoint}, {State: domain.ReportDone}}},
	}
	r, sink := newRecorder(store)
	r.SessionEnded(context.Background(), rec, EndKilled)

	ev := sink.only(t, "ao.sessions.ended")
	want := map[string]any{
		"kind": "worker", "harness": "codex", "outcome": OutcomeCompleted, "had_pr": false,
		"waiting_input_count": int64(3), "duration_ms": int64(90000), "created_by": "orchestrator",
	}
	for k, v := range want {
		if ev.Payload[k] != v {
			t.Errorf("payload[%s] = %#v, want %#v", k, ev.Payload[k], v)
		}
	}
	if len(ev.Payload) != len(want) {
		t.Errorf("payload = %#v, want exactly %d keys (no grouping IDs without an install)", ev.Payload, len(want))
	}
	if ev.SessionID == nil || *ev.SessionID != "w-1" || ev.ProjectID == nil || *ev.ProjectID != "proj" {
		t.Errorf("event ids = %v/%v", ev.SessionID, ev.ProjectID)
	}
}

func TestSessionEndedOmitsUnknownCreator(t *testing.T) {
	rec := domain.SessionRecord{ID: "w-1", Kind: domain.KindWorker, CreatedAt: t0}
	r, sink := newRecorder(&fakeStore{prs: map[domain.SessionID][]domain.PullRequest{"w-1": {{Number: 4}}}})
	r.SessionEnded(context.Background(), rec, EndKilled)
	ev := sink.only(t, "ao.sessions.ended")
	if _, ok := ev.Payload["created_by"]; ok {
		t.Fatalf("created_by present for a session created before it was recorded: %#v", ev.Payload)
	}
	if ev.Payload["outcome"] != OutcomePROpened || ev.Payload["had_pr"] != true {
		t.Fatalf("payload = %#v, want pr_opened with had_pr", ev.Payload)
	}
	if ev.Payload["kind"] != "standalone" {
		t.Fatalf("standalone kind = %#v", ev.Payload["kind"])
	}
}

// The ended event carries the same grouping IDs as ao.usage.messages_sent, so
// a session's outcome joins its message counts.
func TestSessionEndedJoinsUsageGrouping(t *testing.T) {
	const projectTelemetryID = "0b1c2d3e-4f50-4617-8293-a4b5c6d7e8f9"
	store := &fakeStore{projectIDs: map[string]string{"proj|ins_1": projectTelemetryID}}
	r, sink := newRecorder(store)
	r.SetInstallID(func() string { return "ins_1" })
	r.SessionEnded(context.Background(), domain.SessionRecord{ID: "w-1", ProjectID: "proj", Kind: domain.KindWorker}, EndKilled)

	p := sink.only(t, "ao.sessions.ended").Payload
	if p["project_telemetry_id"] != projectTelemetryID || p["session_telemetry_id"] != SessionTelemetryID(projectTelemetryID, "w-1") {
		t.Fatalf("grouping = %#v / %#v", p["project_telemetry_id"], p["session_telemetry_id"])
	}
	if got := SessionTelemetryID(projectTelemetryID, "w-1"); len(got) != 32 {
		t.Fatalf("session_telemetry_id = %q, want 32 hex chars like the usage summary", got)
	}
}

func TestSessionEndedSkipsGroupingWhenUnavailable(t *testing.T) {
	cases := []struct {
		name      string
		installID string
		projectID domain.ProjectID
		lookupErr error
		lookups   int
	}{
		{"export off", "", "proj", nil, 0},
		{"standalone session", "ins_1", "", nil, 0},
		{"lookup failure", "ins_1", "proj", errors.New("db"), 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{projectErr: tc.lookupErr}
			r, sink := newRecorder(store)
			r.SetInstallID(func() string { return tc.installID })
			r.SessionEnded(context.Background(), domain.SessionRecord{ID: "w-1", ProjectID: tc.projectID}, EndKilled)
			p := sink.only(t, "ao.sessions.ended").Payload
			if _, ok := p["project_telemetry_id"]; ok {
				t.Fatalf("payload = %#v, want no grouping", p)
			}
			if store.lookups != tc.lookups {
				t.Fatalf("grouping lookups = %d, want %d (no ID minted while export is off)", store.lookups, tc.lookups)
			}
		})
	}
}

func TestMergedVia(t *testing.T) {
	if got := MergedViaSurface(domain.RequestSurfaceDesktop, true); got != MergedViaDesktop {
		t.Errorf("desktop = %q", got)
	}
	if got := MergedViaSurface(domain.RequestSurfaceMobile, true); got != MergedViaMobile {
		t.Errorf("mobile = %q", got)
	}
	if got := MergedViaSurface(domain.RequestSurfaceCLI, true); got != MergedViaCLI {
		t.Errorf("cli = %q", got)
	}
	if got := MergedViaSurface("", false); got != MergedViaCLI {
		t.Errorf("no surface = %q", got)
	}
	if got := MergedOutsideAO("github"); got != MergedViaGitHub {
		t.Errorf("github = %q", got)
	}
	if got := MergedOutsideAO("gitlab"); got != "" {
		t.Errorf("gitlab = %q", got)
	}
}

func TestNilRecorderIsInert(t *testing.T) {
	var r *Recorder
	r.SetInstallID(func() string { return "ins_1" })
	r.SessionEnded(context.Background(), domain.SessionRecord{ID: "x"}, EndKilled)
}
