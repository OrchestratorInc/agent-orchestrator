package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/deliverytelemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/telemetrymeta"
)

// createdByFakeStore records created_by writes on top of the shared fake.
type createdByFakeStore struct {
	*fakeStore
	createdBy map[domain.SessionID]domain.SessionCreator
}

func (s *createdByFakeStore) RecordSessionCreatedBy(_ context.Context, id domain.SessionID, by domain.SessionCreator) error {
	s.createdBy[id] = by
	return nil
}

func lastEvent(t *testing.T, sink *fakeTelemetrySink, name string) ports.TelemetryEvent {
	t.Helper()
	for i := len(sink.events) - 1; i >= 0; i-- {
		if sink.events[i].Name == name {
			return sink.events[i]
		}
	}
	t.Fatalf("no %s in %#v", name, sink.events)
	return ports.TelemetryEvent{}
}

func TestStandaloneSessionKindsOnSpawnAndFailure(t *testing.T) {
	sink := &fakeTelemetrySink{}
	svc := NewWithDeps(Deps{Manager: &fakeCommander{}, Store: newFakeStore(), Telemetry: sink})
	rec := domain.SessionRecord{ID: "standalone-1", Kind: domain.KindWorker, Harness: domain.HarnessCodex}
	svc.emitSpawned(context.Background(), rec, 1, false, false, domain.SessionCreatedByUser)
	svc.emitSpawnFailed(context.Background(), ports.SpawnConfig{Kind: domain.KindWorker, Harness: domain.HarnessCodex}, errors.New("failed"), 1)
	for _, name := range []string{"ao.session.spawned", "ao.session.spawn_failed"} {
		if got := lastEvent(t, sink, name).Payload["kind"]; got != "standalone" {
			t.Fatalf("%s kind = %#v", name, got)
		}
	}
}

func TestStandaloneKindOnRelaunchFailure(t *testing.T) {
	oldCapture := captureRelaunchError
	captureRelaunchError = func(context.Context, error, map[string]string, string) {}
	t.Cleanup(func() { captureRelaunchError = oldCapture })
	st := newFakeStore()
	st.sessions["standalone-1"] = domain.SessionRecord{ID: "standalone-1", Kind: domain.KindWorker, Harness: domain.HarnessCodex}
	sink := &fakeTelemetrySink{}
	svc := NewWithDeps(Deps{Manager: &fakeCommander{}, Store: st, Telemetry: sink})
	svc.emitRelaunchFailed(context.Background(), "resume_agent", "standalone-1",
		&sessionmanager.RelaunchError{Step: "runtime", Err: errors.New("failed")},
		apierr.Conflict("AGENT_RESUME_FAILED", "failed", nil))
	if got := lastEvent(t, sink, "ao.session.relaunch_failed").Payload["kind"]; got != "standalone" {
		t.Fatalf("relaunch kind = %#v", got)
	}
}

func TestSpawnCreatedByAndSurface(t *testing.T) {
	run := domain.AutomationRunID("automation-run-1")
	cases := []struct {
		name    string
		cfg     ports.SpawnConfig
		surface domain.RequestSurface
		want    domain.SessionCreator
	}{
		{"desktop task", ports.SpawnConfig{Kind: domain.KindWorker}, domain.RequestSurfaceDesktop, domain.SessionCreatedByUser},
		{"cli outside a session", ports.SpawnConfig{Kind: domain.KindWorker}, domain.RequestSurfaceCLI, domain.SessionCreatedByUser},
		{"orchestrator ao spawn", ports.SpawnConfig{Kind: domain.KindWorker, ParentSessionID: "orch-1"}, domain.RequestSurfaceCLI, domain.SessionCreatedByOrchestrator},
		{"worker ao spawn", ports.SpawnConfig{Kind: domain.KindWorker, ParentSessionID: "work-1"}, domain.RequestSurfaceCLI, domain.SessionCreatedByAutomation},
		{"schedule", ports.SpawnConfig{Kind: domain.KindWorker, AutomationRunID: &run}, "", domain.SessionCreatedByScheduled},
		{"tracker intake", ports.SpawnConfig{Kind: domain.KindWorker, CreatedBy: domain.SessionCreatedByAutomation}, "", domain.SessionCreatedByAutomation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := &createdByFakeStore{fakeStore: newFakeStore(), createdBy: map[domain.SessionID]domain.SessionCreator{}}
			st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
			st.sessions["orch-1"] = domain.SessionRecord{ID: "orch-1", ProjectID: "mer", Kind: domain.KindOrchestrator}
			st.sessions["work-1"] = domain.SessionRecord{ID: "work-1", ProjectID: "mer", Kind: domain.KindWorker}
			sink := &fakeTelemetrySink{}
			svc := NewWithDeps(Deps{Manager: &fakeCommander{}, Store: st, Telemetry: sink})
			ctx := context.Background()
			if tc.surface != "" {
				ctx = telemetrymeta.WithSurface(ctx, tc.surface)
			}
			cfg := tc.cfg
			cfg.ProjectID = "mer"
			if _, _, _, err := svc.Spawn(ctx, cfg); err != nil {
				t.Fatalf("Spawn: %v", err)
			}
			ev := lastEvent(t, sink, "ao.session.spawned")
			if ev.Payload["created_by"] != string(tc.want) {
				t.Fatalf("created_by = %#v, want %q", ev.Payload["created_by"], tc.want)
			}
			surface, hasSurface := ev.Payload["surface"]
			if tc.surface == "" && hasSurface {
				t.Fatalf("surface = %#v for a spawn no request started", surface)
			}
			if tc.surface != "" && surface != string(tc.surface) {
				t.Fatalf("surface = %#v, want %q", surface, tc.surface)
			}
			if st.createdBy["mer-9"] != tc.want {
				t.Fatalf("persisted created_by = %q, want %q", st.createdBy["mer-9"], tc.want)
			}
		})
	}
}

// usageSendFakeStore records counted human sends on top of the shared fake.
type usageSendFakeStore struct {
	*fakeStore
	sends []usageSend
}

type usageSend struct {
	id      domain.SessionID
	steer   bool
	surface domain.RequestSurface
}

func (s *usageSendFakeStore) RecordUsageHumanSend(ctx context.Context, id domain.SessionID, steer bool) error {
	surface, _ := telemetrymeta.SurfaceFromContext(ctx)
	s.sends = append(s.sends, usageSend{id: id, steer: steer, surface: surface})
	return nil
}

// Steers and sends are counted into the usage summary, with the request
// surface still on the context the store reads it from; nothing is emitted
// directly.
func TestRecordUserMessageCountsIntoUsageSummary(t *testing.T) {
	st := &usageSendFakeStore{fakeStore: newFakeStore()}
	sink := &fakeTelemetrySink{}
	svc := NewWithDeps(Deps{Manager: &fakeCommander{}, Store: st, Telemetry: sink})

	svc.RecordUserMessage(telemetrymeta.WithSurface(context.Background(), domain.RequestSurfaceMobile), "orch-1", true)
	svc.RecordUserMessage(telemetrymeta.WithSurface(context.Background(), domain.RequestSurfaceCLI), "work-1", false)
	want := []usageSend{{"orch-1", true, domain.RequestSurfaceMobile}, {"work-1", false, domain.RequestSurfaceCLI}}
	if len(st.sends) != len(want) || st.sends[0] != want[0] || st.sends[1] != want[1] {
		t.Fatalf("sends = %+v, want %+v", st.sends, want)
	}
	if len(sink.events) != 0 {
		t.Fatalf("emitted %#v; counts go out with ao.usage.messages_sent", sink.events)
	}
}

// deliveryFakeStore is the recorder's read surface over fixed facts.
type deliveryFakeStore struct {
	prs []domain.PullRequest
}

func (deliveryFakeStore) UsageProjectID(context.Context, string, string) (string, error) {
	return "", nil
}
func (deliveryFakeStore) GetSessionTelemetry(context.Context, domain.SessionID) (domain.SessionTelemetryFacts, error) {
	return domain.SessionTelemetryFacts{CreatedBy: domain.SessionCreatedByUser}, nil
}
func (d deliveryFakeStore) ListPRsBySession(context.Context, domain.SessionID) ([]domain.PullRequest, error) {
	return d.prs, nil
}
func (deliveryFakeStore) ListReportsBySession(context.Context, domain.SessionID) ([]domain.ReportRecord, error) {
	return nil, nil
}

func TestKillReportsSessionEndedOnce(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		CreatedAt: time.Now().Add(-time.Minute), Activity: domain.Activity{State: domain.ActivityActive},
	}
	st.sessions["done"] = domain.SessionRecord{ID: "done", ProjectID: "mer", IsTerminated: true}
	sink := &fakeTelemetrySink{}
	fc := &fakeCommander{}
	svc := NewWithDeps(Deps{Manager: fc, Store: st, Delivery: deliverytelemetry.New(sink, deliveryFakeStore{})})

	if _, err := svc.Kill(context.Background(), "mer-1"); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	ev := lastEvent(t, sink, "ao.sessions.ended")
	if ev.Payload["outcome"] != "killed" || ev.Payload["created_by"] != "user" || ev.Payload["harness"] != "codex" {
		t.Fatalf("payload = %#v, want killed mid-turn by a user-created codex worker", ev.Payload)
	}

	before := len(sink.events)
	if _, err := svc.RequestKill(context.Background(), "done"); err != nil {
		t.Fatalf("RequestKill: %v", err)
	}
	if len(sink.events) != before {
		t.Fatalf("killing an already terminated session reported again: %#v", sink.events[before:])
	}
}
