package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestReplaceCursorUsageReplacesItsRangeAndStaysOutOfTheCollector(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	sess := seedUsageSession(t, s, domain.HarnessCursor)
	sess.Metadata.RuntimeLaunchID = "launch-current"
	mustNoError(t, s.UpdateSession(ctx, sess))

	if s.CursorUsageEnabled(ctx) {
		t.Fatal("Cursor usage is read before the user turns it on")
	}
	mustNoError(t, s.SetCursorUsage(ctx, true, time.Now()))
	if !s.CursorUsageEnabled(ctx) {
		t.Fatal("Cursor usage stayed off after it was turned on")
	}

	base := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	event := func(key string, at time.Time, cost int64) domain.CursorSessionEvent {
		one, zero := int64(1), int64(0)
		return domain.CursorSessionEvent{SessionID: sess.ID, ConversationID: "conv", Event: domain.ModelUsageEvent{
			ProviderID: domain.UsageProviderOpenAI, BillingProviderID: "cursor",
			BillingProviderSource: domain.UsageBillingProviderObserved, ModelID: "gpt-5",
			MeasurementKind: domain.UsageMeasurementNativeReported,
			Tokens:          domain.UsageTokenMetrics{InputTokens: &one, CachedInputTokens: &zero, UncachedInputTokens: &one, OutputTokens: &one},
			CreatedAt:       at, SourceEventKey: key,
			Costs: domain.UsageEventCosts{EstimatedCostNanos: &cost, PricingVersion: "cursor:reported"},
		}}
	}
	read := func(since, until time.Time, events ...domain.CursorSessionEvent) {
		t.Helper()
		mustNoError(t, s.ReplaceCursorUsage(ctx, domain.CursorUsageRead{
			AccountKey: "account", Since: since, Until: until, Events: events, Now: until,
		}))
	}
	read(base, base.Add(2*time.Hour), event("early", base.Add(10*time.Minute), 100), event("late", base.Add(90*time.Minute), 200))
	read(base.Add(time.Hour), base.Add(3*time.Hour), event("late-final", base.Add(90*time.Minute), 250))

	events, err := s.ListUsageReportEvents(ctx, base, base.Add(4*time.Hour))
	mustNoError(t, err)
	var total int64
	for _, event := range events {
		if event.Harness != domain.HarnessCursor || event.SessionID != sess.ID {
			t.Fatalf("event belongs to %s %s, want the Cursor session", event.Harness, event.SessionID)
		}
		total += *event.Costs.EstimatedCostNanos
	}
	if len(events) != 2 || total != 350 {
		t.Fatalf("stored %d events costing %d, want early 100 + late-final 250", len(events), total)
	}
	account, until, ok, err := s.GetUsageCursorRead(ctx)
	if err != nil || !ok || account != "account" || !until.Equal(base.Add(3*time.Hour)) {
		t.Fatalf("read mark = %q %v %v %v, want account at %v", account, until, ok, err, base.Add(3*time.Hour))
	}

	watchable, err := s.ListWatchableUsageSources(ctx)
	mustNoError(t, err)
	for _, source := range watchable {
		if source.Kind == domain.UsageSourceCursorDashboard {
			t.Fatalf("Cursor source %+v is watched as a file", source)
		}
	}
	reloaded, found, err := s.GetSession(ctx, sess.ID)
	if err != nil || !found {
		t.Fatalf("reload session: %v %v", found, err)
	}
	finalized, err := s.FinalizeUsageBindingsForSessionLaunch(ctx, sess.ID, "launch-current", reloaded.Revision, base.Add(4*time.Hour))
	if err != nil || len(finalized) != 0 {
		t.Fatalf("finalization took Cursor bindings %+v: %v", finalized, err)
	}
	binding, ok, err := s.GetUsageBinding(ctx, sess.ID, domain.HarnessCursor, "conv")
	if err != nil || !ok || binding.State != domain.UsageBindingComplete {
		t.Fatalf("Cursor binding = %+v %v %v, want it complete", binding, ok, err)
	}
}
