package sentryobs

import (
	"context"
	"errors"
	"testing"

	"github.com/getsentry/sentry-go"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/telemetrymeta"
)

func TestPaidPlanSendsOutsideTheSampleAndTagsEveryEvent(t *testing.T) {
	transport := &recordingTransport{events: make(chan *sentry.Event, 4)}
	if InstallInErrorSample("ins_unsampled_0") {
		t.Fatal("fixture install must be outside the sample")
	}
	if err := Init(Config{DSN: "https://public@example.com/1", InstallID: "ins_unsampled_0", Release: "2.0.0", Environment: "stable", Transport: transport}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		SetPlan("unknown")
		finishBackgroundCaptureTest(t)
	})
	SetPolicyCheck(func(context.Context) bool { return true })

	for _, plan := range []string{"free", "none", "unknown", "Pro Plan $49"} {
		SetPlan(plan)
		CaptureBackground(context.Background(), errors.New("boom"), "reaper", "reaper_tick", nil)
	}
	select {
	case event := <-transport.events:
		t.Fatalf("an unpaid install outside the sample sent %+v", event.Tags)
	default:
	}

	SetPlan("pro")
	CaptureBackground(context.Background(), errors.New("boom"), "reaper", "reaper_tick", nil)
	event := <-transport.events
	if event.Tags["plan"] != "pro" || event.Tags["sample_rate"] != fatalErrorRate {
		t.Fatalf("paid capture tags = %+v", event.Tags)
	}

	// The plan read at admission rides the event: a change before the send
	// cannot split admission, the plan tag and sample_rate.
	SetPlan("pro")
	admitted, send := admitPlan(map[string]string{"component": "daemon"})
	SetPlan("free")
	if !send {
		t.Fatal("pro event was not admitted")
	}
	capture(sentry.LevelError, admitted, "", telemetrymeta.SafeRoute{}, telemetrymeta.FailureSite{}, false)
	event = <-transport.events
	if event.Tags["plan"] != "pro" || event.Tags["sample_rate"] != fatalErrorRate {
		t.Fatalf("snapshot tags = %+v", event.Tags)
	}

	SetPlan("Acme Corp")
	if currentPlan() != "unknown" || !safeSentryTag("plan", "enterprise") || safeSentryTag("plan", "Acme Corp") {
		t.Fatal("plan outside the vocabulary was kept")
	}
}
