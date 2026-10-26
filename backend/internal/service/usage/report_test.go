package usage

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/pricing"
)

type reportStoreStub struct {
	events       []domain.UsageReportEvent
	since, until time.Time
}

func (s *reportStoreStub) ListUsageReportEvents(_ context.Context, since, until time.Time) ([]domain.UsageReportEvent, error) {
	s.since, s.until = since, until
	return s.events, nil
}

type snapshotStub struct{ snapshot *pricing.Snapshot }

func (s snapshotStub) Snapshot() *pricing.Snapshot { return s.snapshot }

// repoCatalog loads the reviewed catalog the repository ships, which carries
// the tier rows these tests price against.
func repoCatalog(t *testing.T) *pricing.Snapshot {
	t.Helper()
	catalog, err := pricing.NewCache("../../../..").Load(context.Background())
	if err != nil {
		t.Fatalf("load repository catalog: %v", err)
	}
	return catalog.Snapshot()
}

func pricedEvent(t *testing.T, snapshot *pricing.Snapshot, event domain.UsageReportEvent, providerUsage string) domain.UsageReportEvent {
	t.Helper()
	cached, uncached := tokenValue(event.Tokens.CachedInputTokens), tokenValue(event.Tokens.UncachedInputTokens)
	input := cached + uncached
	estimate, err := snapshot.Estimate(domain.ModelUsageEvent{
		ProviderID: event.ProviderID, BillingProviderID: event.BillingProviderID, ModelID: event.ModelID, ServiceTier: event.ServiceTier,
		Tokens:            domain.UsageTokenMetrics{InputTokens: &input, CachedInputTokens: event.Tokens.CachedInputTokens, UncachedInputTokens: event.Tokens.UncachedInputTokens, OutputTokens: event.Tokens.OutputTokens},
		ProviderUsageJSON: providerUsage,
	})
	if err != nil || estimate.TotalNanos == nil {
		t.Fatalf("estimate %s: %+v, %v", event.ModelID, estimate, err)
	}
	event.Costs = domain.UsageEventCosts{
		InputCostNanos: estimate.InputNanos, CachedInputCostNanos: estimate.CachedInputNanos,
		OutputCostNanos: estimate.OutputNanos, EstimatedCostNanos: estimate.TotalNanos,
	}
	return event
}

func int64p(value int64) *int64 { return &value }

// Break caught: hour buckets misplaced in a half-hour time zone, a window that
// drops today, a priority premium or cache-write share that does not add up
// to the stored cost, or an unpriced event counted as free.
func TestReportBucketsSplitsAndFilters(t *testing.T) {
	snapshot := repoCatalog(t)
	kolkata, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 25, 20, 40, 0, 0, kolkata)
	at := func(day, hour, minute int) time.Time {
		return time.Date(2026, 10, day, hour, minute, 0, 0, kolkata).UTC()
	}

	codex := pricedEvent(t, snapshot, domain.UsageReportEvent{
		CreatedAt: at(25, 19, 10), ProviderID: domain.UsageProviderOpenAI, BillingProviderID: "openai", ModelID: "gpt-6.1-sol", ServiceTier: "priority",
		Tokens:  domain.UsageTokenMetrics{CachedInputTokens: int64p(9000), UncachedInputTokens: int64p(1000), OutputTokens: int64p(100)},
		Harness: domain.HarnessCodex, SessionID: "app-1", ProjectID: "app", SessionTitle: "Fix login",
		CacheWriteTokens: int64p(0),
	}, `{"last_token_usage":{"cache_write_input_tokens":0}}`)
	claude := pricedEvent(t, snapshot, domain.UsageReportEvent{
		CreatedAt: at(24, 9, 0), ProviderID: domain.UsageProviderAnthropic, BillingProviderID: "anthropic", ModelID: "claude-opus-5-5",
		Tokens:  domain.UsageTokenMetrics{CachedInputTokens: int64p(5000), UncachedInputTokens: int64p(2000), OutputTokens: int64p(50)},
		Harness: domain.HarnessClaudeCode, SessionID: "app-2", ProjectID: "app", SessionTitle: "Add tests",
		CacheWriteTokens: int64p(1500), CacheWrite5mTokens: int64p(0), CacheWrite1hTokens: int64p(1500),
	}, `{"cache_creation_input_tokens":1500,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":1500}}`)
	unpriced := domain.UsageReportEvent{
		CreatedAt: at(25, 8, 0), ProviderID: domain.UsageProviderOpenAI, BillingProviderID: "openai", ModelID: "glm-unknown",
		Tokens:  domain.UsageTokenMetrics{CachedInputTokens: int64p(0), UncachedInputTokens: int64p(300), OutputTokens: int64p(20)},
		Harness: domain.HarnessCodex, SessionID: "app-1", ProjectID: "app",
	}
	otherProject := codex
	otherProject.ProjectID, otherProject.SessionID = "web", "web-1"

	store := &reportStoreStub{events: []domain.UsageReportEvent{codex, claude, unpriced, otherProject}}
	reader := NewReportReader(store, snapshotStub{snapshot})
	reader.now = func() time.Time { return now }

	report, err := reader.Report(context.Background(), 7, "Asia/Kolkata", "app")
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if len(report.Periods) != 7 || report.Periods[0] != "2026-10-19" || report.Periods[6] != "2026-10-25" || report.Resolution != "day" {
		t.Fatalf("periods = %v (%s)", report.Periods, report.Resolution)
	}
	if !store.since.Equal(time.Date(2026, 10, 19, 0, 0, 0, 0, kolkata)) || !store.until.Equal(time.Date(2026, 10, 26, 0, 0, 0, 0, kolkata)) {
		t.Fatalf("window = %s .. %s", store.since, store.until)
	}

	rows := map[string]domain.UsageReportRow{}
	for _, row := range report.Rows {
		rows[row.Period+"/"+row.ModelID] = row
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %+v, want codex, claude and unpriced in project app only", report.Rows)
	}
	priority := rows["2026-10-25/gpt-6.1-sol"]
	if priority.Speed != "priority" || priority.Events != 1 || priority.PricedEvents != 1 || priority.CacheReadTokens != 9000 || priority.InputTokens != 1000 {
		t.Fatalf("priority row = %+v", priority)
	}
	if priority.CostNanos != *codex.Costs.EstimatedCostNanos || priority.PremiumNanos*2 != priority.CostNanos {
		t.Fatalf("priority cost %d premium %d, want premium half of a 2x rate", priority.CostNanos, priority.PremiumNanos)
	}
	if priority.CacheSavingsNanos <= 0 {
		t.Fatalf("priority cache savings = %d, want positive", priority.CacheSavingsNanos)
	}

	opus := rows["2026-10-24/claude-opus-5-5"]
	if opus.Speed != "standard" || opus.PremiumNanos != 0 || opus.CacheWriteTokens != 1500 || opus.InputTokens != 500 {
		t.Fatalf("opus row = %+v", opus)
	}
	if opus.CacheWriteCostNanos <= 0 || opus.InputCostNanos+opus.CacheWriteCostNanos+opus.CacheReadCostNanos+opus.OutputCostNanos != opus.CostNanos {
		t.Fatalf("opus categories = %+v, want a cache-write part and a sum equal to the stored cost", opus)
	}

	free := rows["2026-10-25/glm-unknown"]
	if free.Events != 1 || free.PricedEvents != 0 || free.CostNanos != 0 || free.InputTokens != 300 {
		t.Fatalf("unpriced row = %+v", free)
	}
	if len(report.Sessions) != 2 || report.Sessions[0].Title == "" {
		t.Fatalf("sessions = %+v", report.Sessions)
	}

	hourly, err := reader.Report(context.Background(), 1, "Asia/Kolkata", "")
	if err != nil {
		t.Fatalf("hourly Report: %v", err)
	}
	if len(hourly.Periods) != 24 || hourly.Periods[23] != "2026-10-25T20:00" || hourly.Resolution != "hour" {
		t.Fatalf("hourly periods = %v", hourly.Periods)
	}
	if !store.until.Equal(time.Date(2026, 10, 25, 21, 0, 0, 0, kolkata)) {
		t.Fatalf("hourly window ends %s, want the end of the current local hour", store.until)
	}

	if _, err := reader.Report(context.Background(), 7, "Mars/Olympus", ""); err == nil {
		t.Fatal("unknown time zone accepted")
	}
	if _, err := reader.Report(context.Background(), 0, "UTC", ""); err == nil {
		t.Fatal("zero-day window accepted")
	}
}
