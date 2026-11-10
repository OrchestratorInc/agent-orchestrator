package usage

import (
	"context"
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

// ReportReader builds the usage page's report: tokens and estimated cost over a window, bucketed by day or hour in the reader's time zone.
type ReportReader struct {
	store   usageReportStore
	pricing pricingSnapshots
	now     func() time.Time
}

// NewReportReader constructs a report reader.
func NewReportReader(store usageReportStore, prices pricingSnapshots) *ReportReader {
	return &ReportReader{store: store, pricing: prices, now: time.Now}
}

const (
	reportDayLayout  = "2006-01-02"
	reportHourLayout = "2006-01-02T15:00"
)

// Report reads the last days of usage.
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
		if costs.EstimatedCostNanos == nil {
			continue
		}
		split := splits[priceKeyOf(event)]
		total := *costs.EstimatedCostNanos
		row.PricedEvents++
		if costs.InputCostNanos != nil && costs.CachedInputCostNanos != nil && costs.OutputCostNanos != nil {
			input := *costs.InputCostNanos
			writeCost := min(roundNanos(float64(input)*split.writeShare), input)
			row.InputCostNanos += input - writeCost
			row.CacheWriteCostNanos += writeCost
			row.CacheReadCostNanos += *costs.CachedInputCostNanos
			row.OutputCostNanos += *costs.OutputCostNanos
		}
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

type priceKey struct {
	provider domain.UsageProviderID
	billing  string
	model    string
	tier     string
}

func priceKeyOf(event domain.UsageReportEvent) priceKey {
	return priceKey{provider: event.ProviderID, billing: event.BillingProviderID, model: event.ModelID, tier: event.ServiceTier}
}

type costSplit struct {
	writeShare            float64
	premiumShare          float64
	savingsPerCachedToken float64
}

func costSplits(snapshot *pricing.Snapshot, events []domain.UsageReportEvent) map[priceKey]costSplit {
	type tokens struct{ cached, uncached, output, write, write1h int64 }
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
		sum.write1h += min(max(tokenValue(event.CacheWrite1hTokens), 0), write)
	}
	out := make(map[priceKey]costSplit, len(sums))
	if snapshot == nil {
		return out
	}
	for key, sum := range sums {
		event := func(tier string, cached, uncached int64) domain.ModelUsageEvent {
			return domain.ModelUsageEvent{
				ProviderID: key.provider, BillingProviderID: key.billing, ModelID: key.model, ServiceTier: tier,
				Tokens: domain.UsageTokenMetrics{
					CachedInputTokens: &cached, UncachedInputTokens: &uncached, OutputTokens: &sum.output,
				},
				ProviderUsageJSON: syntheticProviderUsage(sum.write, sum.write1h),
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

func syntheticProviderUsage(write, write1h int64) string {
	return fmt.Sprintf(`{"ao_derived_cache_write_input_tokens":%d,"ao_derived_cache_write_1h_input_tokens":%d}`, write, write1h)
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
