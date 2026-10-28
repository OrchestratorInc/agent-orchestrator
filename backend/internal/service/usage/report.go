package usage

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/pricing"
)

type usageReportStore interface {
	ListUsageReportEvents(context.Context, time.Time, time.Time) ([]domain.UsageReportEvent, error)
}

type pricingSnapshots interface {
	Snapshot() *pricing.Snapshot
}

// ReportReader builds the usage page's report: tokens and estimated cost over a
// window, bucketed by day or hour in the reader's time zone.
type ReportReader struct {
	store   usageReportStore
	pricing pricingSnapshots
	now     func() time.Time
}

// NewReportReader constructs a report reader. Without a pricing source the
// report still carries every stored cost; it only cannot split out cache
// writes, speed premiums and cache savings.
func NewReportReader(store usageReportStore, prices pricingSnapshots) *ReportReader {
	return &ReportReader{store: store, pricing: prices, now: time.Now}
}

const (
	reportDayLayout  = "2006-01-02"
	reportHourLayout = "2006-01-02T15:00"
)

// Report reads the last days of usage. One day reads the last 24 hours by hour;
// more read whole days, today included. offset steps back that many whole
// windows, so offset 1 with 7 days is the week before this one. An empty
// projectID reads every project.
func (r *ReportReader) Report(ctx context.Context, days, offset int, timeZone string, projectID domain.ProjectID) (domain.UsageReport, error) {
	if r == nil || r.store == nil {
		return domain.UsageReport{}, fmt.Errorf("usage report store is unavailable")
	}
	if days < 1 || days > 90 {
		return domain.UsageReport{}, apierr.Invalid("INVALID_USAGE_WINDOW", "days must be between 1 and 90", nil)
	}
	if offset < 0 {
		return domain.UsageReport{}, apierr.Invalid("INVALID_USAGE_WINDOW", "offset must not be negative", nil)
	}
	zone := strings.TrimSpace(timeZone)
	if zone == "" {
		zone = "UTC"
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return domain.UsageReport{}, apierr.Invalid("INVALID_TIME_ZONE", "Unknown time zone "+zone, nil)
	}
	report := domain.UsageReport{Days: days, TimeZone: zone}
	now := r.now().In(loc)
	if days == 1 {
		now = now.Add(-24 * time.Hour * time.Duration(offset))
	} else {
		now = now.AddDate(0, 0, -days*offset)
	}
	var periodOf func(time.Time) string
	if days == 1 {
		report.Resolution = "hour"
		// time.Truncate works on absolute time, which misplaces the hour in
		// zones with a half-hour offset, so the boundary is built locally.
		report.Until = time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 0, 0, 0, loc).Add(time.Hour)
		report.Since = report.Until.Add(-24 * time.Hour)
		periodOf = func(t time.Time) string { return t.In(loc).Format(reportHourLayout) }
		for t := report.Since; t.Before(report.Until); t = t.Add(time.Hour) {
			report.Periods = append(report.Periods, periodOf(t))
		}
	} else {
		report.Resolution = "day"
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
		report.Since = today.AddDate(0, 0, -(days - 1))
		report.Until = today.AddDate(0, 0, 1)
		periodOf = func(t time.Time) string { return t.In(loc).Format(reportDayLayout) }
		for t := report.Since; t.Before(report.Until); t = t.AddDate(0, 0, 1) {
			report.Periods = append(report.Periods, periodOf(t))
		}
	}

	events, err := r.store.ListUsageReportEvents(ctx, report.Since, report.Until)
	if err != nil {
		return domain.UsageReport{}, err
	}
	if projectID != "" {
		kept := events[:0]
		for _, event := range events {
			if event.ProjectID == projectID {
				kept = append(kept, event)
			}
		}
		events = kept
	}

	var snapshot *pricing.Snapshot
	if r.pricing != nil {
		snapshot = r.pricing.Snapshot()
	}
	splits := costSplits(snapshot, events)

	type rowKey struct {
		period, model, speed string
		harness              domain.AgentHarness
	}
	rows := map[rowKey]*domain.UsageReportRow{}
	sessions := map[domain.SessionID]*domain.UsageReportSession{}
	for _, event := range events {
		cached, uncached, output := tokenValue(event.Tokens.CachedInputTokens), tokenValue(event.Tokens.UncachedInputTokens), tokenValue(event.Tokens.OutputTokens)
		write := min(max(tokenValue(event.CacheWriteTokens), 0), uncached)
		key := rowKey{
			period: periodOf(event.CreatedAt), harness: event.Harness, model: event.ModelID,
			speed: pricing.SpeedTier(event.ProviderID, event.ServiceTier),
		}
		row := rows[key]
		if row == nil {
			row = &domain.UsageReportRow{Period: key.period, Harness: key.harness, ModelID: key.model, Speed: key.speed}
			rows[key] = row
		}
		row.Events++
		row.InputTokens += uncached - write
		row.CacheReadTokens += cached
		row.CacheWriteTokens += write
		row.OutputTokens += output

		session := sessions[event.SessionID]
		if session == nil {
			session = &domain.UsageReportSession{
				SessionID: event.SessionID, ProjectID: event.ProjectID, Harness: event.Harness, Title: event.SessionTitle,
			}
			sessions[event.SessionID] = session
		}
		session.Events++
		session.Tokens += cached + uncached + output
		if event.CreatedAt.After(session.LastUsedAt) {
			session.LastUsedAt = event.CreatedAt
		}

		costs := event.Costs
		if costs.EstimatedCostNanos == nil || costs.InputCostNanos == nil || costs.CachedInputCostNanos == nil || costs.OutputCostNanos == nil {
			continue
		}
		split := splits[priceKeyOf(event)]
		total, input := *costs.EstimatedCostNanos, *costs.InputCostNanos
		writeCost := min(roundNanos(float64(input)*split.writeShare), input)
		row.PricedEvents++
		row.InputCostNanos += input - writeCost
		row.CacheWriteCostNanos += writeCost
		row.CacheReadCostNanos += *costs.CachedInputCostNanos
		row.OutputCostNanos += *costs.OutputCostNanos
		row.CostNanos += total
		row.PremiumNanos += roundNanos(float64(total) * split.premiumShare)
		row.CacheSavingsNanos += roundNanos(float64(cached) * split.savingsPerCachedToken)
		session.PricedEvents++
		session.CostNanos += total
	}

	report.Rows = make([]domain.UsageReportRow, 0, len(rows))
	for _, row := range rows {
		report.Rows = append(report.Rows, *row)
	}
	sort.Slice(report.Rows, func(i, j int) bool {
		a, b := report.Rows[i], report.Rows[j]
		if a.Period != b.Period {
			return a.Period < b.Period
		}
		if a.Harness != b.Harness {
			return a.Harness < b.Harness
		}
		if a.ModelID != b.ModelID {
			return a.ModelID < b.ModelID
		}
		return a.Speed < b.Speed
	})
	report.Sessions = make([]domain.UsageReportSession, 0, len(sessions))
	for _, session := range sessions {
		report.Sessions = append(report.Sessions, *session)
	}
	sort.Slice(report.Sessions, func(i, j int) bool {
		a, b := report.Sessions[i], report.Sessions[j]
		if a.CostNanos != b.CostNanos {
			return a.CostNanos > b.CostNanos
		}
		return a.SessionID < b.SessionID
	})
	return report, nil
}

// priceKey groups events that one catalog row prices the same way.
type priceKey struct {
	provider domain.UsageProviderID
	billing  string
	model    string
	tier     string
}

func priceKeyOf(event domain.UsageReportEvent) priceKey {
	return priceKey{provider: event.ProviderID, billing: event.BillingProviderID, model: event.ModelID, tier: event.ServiceTier}
}

// costSplit says how a stored cost divides for display. Stored events keep only
// input, cache-read and output components, so the cache-write part of input,
// the speed premium and the cache savings are worked out from the catalog over
// each price key's summed tokens, then applied to the stored totals. The parts
// therefore always add up to what was stored.
//
// ponytail: one split per price key, not per event; store a cache-write cost
// column per event if a key's events ever need different splits.
type costSplit struct {
	writeShare            float64
	premiumShare          float64
	savingsPerCachedToken float64
}

func costSplits(snapshot *pricing.Snapshot, events []domain.UsageReportEvent) map[priceKey]costSplit {
	type tokens struct{ cached, uncached, output, write, write5m, write1h int64 }
	sums := map[priceKey]*tokens{}
	for _, event := range events {
		if event.Costs.EstimatedCostNanos == nil {
			continue
		}
		key := priceKeyOf(event)
		sum := sums[key]
		if sum == nil {
			sum = &tokens{}
			sums[key] = sum
		}
		uncached := tokenValue(event.Tokens.UncachedInputTokens)
		write := min(max(tokenValue(event.CacheWriteTokens), 0), uncached)
		sum.cached += tokenValue(event.Tokens.CachedInputTokens)
		sum.uncached += uncached
		sum.output += tokenValue(event.Tokens.OutputTokens)
		sum.write += write
		// ponytail: a write without a reported 5m/1h split counts as 5m, the
		// API's default lifetime; read the split from the transcript if this
		// ever matters for old rows.
		oneHour := min(max(tokenValue(event.CacheWrite1hTokens), 0), write)
		sum.write1h += oneHour
		sum.write5m += write - oneHour
	}
	out := make(map[priceKey]costSplit, len(sums))
	if snapshot == nil {
		return out
	}
	for key, sum := range sums {
		event := func(tier string, cached, uncached int64) domain.ModelUsageEvent {
			input := cached + uncached
			return domain.ModelUsageEvent{
				ProviderID: key.provider, BillingProviderID: key.billing, ModelID: key.model, ServiceTier: tier,
				Tokens: domain.UsageTokenMetrics{
					InputTokens: &input, CachedInputTokens: &cached, UncachedInputTokens: &uncached, OutputTokens: &sum.output,
				},
				ProviderUsageJSON: syntheticProviderUsage(key.provider, sum.write, sum.write5m, sum.write1h),
			}
		}
		actual, err := snapshot.Estimate(event(key.tier, sum.cached, sum.uncached))
		if err != nil || actual.TotalNanos == nil || *actual.TotalNanos <= 0 {
			continue
		}
		total := float64(*actual.TotalNanos)
		var split costSplit
		if actual.CacheWriteNanos != nil && actual.InputNanos != nil && *actual.InputNanos > 0 {
			split.writeShare = float64(*actual.CacheWriteNanos) / float64(*actual.InputNanos)
		}
		if pricing.SpeedTier(key.provider, key.tier) != pricing.SpeedStandard {
			if standard, err := snapshot.Estimate(event("", sum.cached, sum.uncached)); err == nil && standard.TotalNanos != nil {
				split.premiumShare = max(total-float64(*standard.TotalNanos), 0) / total
			}
		}
		if sum.cached > 0 {
			if uncachedOnly, err := snapshot.Estimate(event(key.tier, 0, sum.cached+sum.uncached)); err == nil && uncachedOnly.TotalNanos != nil {
				split.savingsPerCachedToken = max(float64(*uncachedOnly.TotalNanos)-total, 0) / float64(sum.cached)
			}
		}
		out[key] = split
	}
	return out
}

// syntheticProviderUsage writes summed cache-write counts in the shape each
// provider's stored usage object uses, so the estimator splits them as it
// would a single event.
func syntheticProviderUsage(provider domain.UsageProviderID, write, write5m, write1h int64) string {
	var object any
	switch provider {
	case domain.UsageProviderAnthropic:
		object = map[string]any{
			"cache_creation_input_tokens": write,
			"cache_creation":              map[string]int64{"ephemeral_5m_input_tokens": write5m, "ephemeral_1h_input_tokens": write1h},
		}
	case domain.UsageProviderOpenAI:
		object = map[string]any{"last_token_usage": map[string]int64{"cache_write_input_tokens": write}}
	default:
		return ""
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func tokenValue(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func roundNanos(value float64) int64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return int64(math.Round(value))
}
