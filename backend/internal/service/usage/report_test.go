package usage

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/pricing"
	"github.com/aoagents/agent-orchestrator/backend/internal/pricing/catalogsync"
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

const reportPriceTable = `{
"gpt-6.1-sol":{"litellm_provider":"openai","mode":"responses","input_cost_per_token":0.000005,"output_cost_per_token":0.00003,"cache_read_input_token_cost":0.0000005,
  "input_cost_per_token_priority":0.00001,"output_cost_per_token_priority":0.00006,"cache_read_input_token_cost_priority":0.000001},
"gpt-5.5":{"litellm_provider":"openai","mode":"responses","input_cost_per_token":0.000005,"output_cost_per_token":0.00003,"cache_read_input_token_cost":0.0000005,
  "input_cost_per_token_above_272k_tokens":0.00001,"output_cost_per_token_above_272k_tokens":0.000045,"cache_read_input_token_cost_above_272k_tokens":0.000001},
"claude-opus-5-5":{"litellm_provider":"anthropic","mode":"chat","input_cost_per_token":0.000005,"output_cost_per_token":0.000025,"cache_read_input_token_cost":0.0000005,
  "cache_creation_input_token_cost":0.00000625,"cache_creation_input_token_cost_above_1hr":0.00001},
"glm-test":{"litellm_provider":"zai","mode":"chat","input_cost_per_token":0.0000006,"output_cost_per_token":0.0000022}
}`

func testCatalog(t *testing.T) *pricing.Snapshot {
	t.Helper()
	catalog, err := catalogsync.Catalog([]byte(reportPriceTable))
	if err != nil {
		t.Fatalf("build test catalog: %v", err)
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

func TestReportBucketsSplitsAndFilters(t *testing.T) {
	snapshot := testCatalog(t)
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
		CacheWriteTokens: int64p(1500), CacheWrite1hTokens: int64p(1500),
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

	report, err := reader.Report(context.Background(), 7, 0, "Asia/Kolkata", "app")
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

	hourly, err := reader.Report(context.Background(), 1, 0, "Asia/Kolkata", "")
	if err != nil {
		t.Fatalf("hourly Report: %v", err)
	}
	if len(hourly.Periods) != 24 || hourly.Periods[23] != "2026-10-25T20:00" || hourly.Resolution != "hour" {
		t.Fatalf("hourly periods = %v", hourly.Periods)
	}
	if !store.until.Equal(time.Date(2026, 10, 25, 21, 0, 0, 0, kolkata)) {
		t.Fatalf("hourly window ends %s, want the end of the current local hour", store.until)
	}

	if _, err := reader.Report(context.Background(), 7, 0, "Mars/Olympus", ""); err == nil {
		t.Fatal("unknown time zone accepted")
	}
	if _, err := reader.Report(context.Background(), 0, 0, "UTC", ""); err == nil {
		t.Fatal("zero-day window accepted")
	}
	if _, err := reader.Report(context.Background(), 7, -1, "UTC", ""); err == nil {
		t.Fatal("negative offset accepted")
	}

	previous, err := reader.Report(context.Background(), 7, 1, "Asia/Kolkata", "")
	if err != nil {
		t.Fatalf("previous week Report: %v", err)
	}
	if previous.Periods[0] != "2026-10-12" || previous.Periods[6] != "2026-10-18" {
		t.Fatalf("previous week periods = %v, want Oct 12-18", previous.Periods)
	}
	if !store.until.Equal(time.Date(2026, 10, 19, 0, 0, 0, 0, kolkata)) {
		t.Fatalf("previous week ends %s, want the start of this week", store.until)
	}
}

func TestReportCountsReportedTotalWithoutSplit(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	event := func(model string, cost int64) domain.UsageReportEvent {
		return domain.UsageReportEvent{
			CreatedAt: now.Add(-time.Hour), ProviderID: domain.UsageProviderOpenAI, BillingProviderID: "opencode", ModelID: model,
			Tokens:  domain.UsageTokenMetrics{CachedInputTokens: int64p(0), UncachedInputTokens: int64p(1000), OutputTokens: int64p(10)},
			Harness: domain.HarnessOpenCode, SessionID: "oc-1", ProjectID: "app",
			Costs: domain.UsageEventCosts{EstimatedCostNanos: int64p(cost), PricingVersion: "opencode:reported"},
		}
	}
	store := &reportStoreStub{events: []domain.UsageReportEvent{event("paid-model", 7_591_330), event("big-pickle", 0)}}
	reader := NewReportReader(store, snapshotStub{testCatalog(t)})
	reader.now = func() time.Time { return now }

	report, err := reader.Report(context.Background(), 1, 0, "UTC", "app")
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	costs := map[string]int64{}
	for _, row := range report.Rows {
		if row.PricedEvents != 1 {
			t.Fatalf("row %s priced events = %d, want 1", row.ModelID, row.PricedEvents)
		}
		costs[row.ModelID] = row.CostNanos
	}
	if costs["paid-model"] != 7_591_330 || costs["big-pickle"] != 0 || len(costs) != 2 {
		t.Fatalf("row costs = %v", costs)
	}
	if len(report.Sessions) != 1 || report.Sessions[0].PricedEvents != 2 || report.Sessions[0].CostNanos != 7_591_330 {
		t.Fatalf("sessions = %+v", report.Sessions)
	}
}

func TestReportCacheSavingsIgnoreSummedLongContext(t *testing.T) {
	snapshot := testCatalog(t)
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	const providerUsage = `{"last_token_usage":{"cache_write_input_tokens":0}}`
	request := pricedEvent(t, snapshot, domain.UsageReportEvent{
		CreatedAt: now.Add(-30 * time.Minute), ProviderID: domain.UsageProviderOpenAI, BillingProviderID: "openai", ModelID: "gpt-5.5",
		Tokens:  domain.UsageTokenMetrics{CachedInputTokens: int64p(180_000), UncachedInputTokens: int64p(20_000), OutputTokens: int64p(100)},
		Harness: domain.HarnessCodex, SessionID: "app-1", ProjectID: "app", CacheWriteTokens: int64p(0),
	}, providerUsage)
	input := int64(200_000)
	uncachedOnly, err := snapshot.Estimate(domain.ModelUsageEvent{
		ProviderID: domain.UsageProviderOpenAI, BillingProviderID: "openai", ModelID: "gpt-5.5",
		Tokens: domain.UsageTokenMetrics{
			InputTokens: &input, CachedInputTokens: int64p(0), UncachedInputTokens: &input, OutputTokens: int64p(100),
		},
		ProviderUsageJSON: providerUsage,
	})
	if err != nil || uncachedOnly.TotalNanos == nil {
		t.Fatalf("uncached estimate: %+v, %v", uncachedOnly, err)
	}
	saved := *uncachedOnly.TotalNanos - *request.Costs.EstimatedCostNanos

	reader := NewReportReader(&reportStoreStub{events: []domain.UsageReportEvent{request, request}}, snapshotStub{snapshot})
	reader.now = func() time.Time { return now }
	report, err := reader.Report(context.Background(), 1, 0, "UTC", "")
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if len(report.Rows) != 1 {
		t.Fatalf("rows = %+v", report.Rows)
	}
	if got := report.Rows[0].CacheSavingsNanos; got < 2*saved-2 || got > 2*saved+2 {
		t.Fatalf("cache savings = %d, want %d at the base row's rates", got, 2*saved)
	}
}
