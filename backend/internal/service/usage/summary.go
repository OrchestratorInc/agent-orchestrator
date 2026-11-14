package usage

import (
	"context"
	"fmt"
	"math"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type usageSummaryStore interface {
	ListCompactSessionUsageAggregates(context.Context, domain.ProjectID) ([]domain.CompactSessionUsageAggregate, error)
}

// SummaryReader derives token and estimated-cost summaries from normalized
// usage events.
type SummaryReader struct{ store usageSummaryStore }

// NewSummaryReader constructs a usage summary reader.
func NewSummaryReader(store usageSummaryStore) *SummaryReader { return &SummaryReader{store: store} }

// ListCompact returns one batch suitable for dashboard cards.
func (r *SummaryReader) ListCompact(ctx context.Context, projectID domain.ProjectID) ([]domain.CompactSessionUsage, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("usage summary store is unavailable")
	}
	rows, err := r.store.ListCompactSessionUsageAggregates(ctx, projectID)
	if err != nil {
		return nil, err
	}
	out := make([]domain.CompactSessionUsage, 0, len(rows))
	for _, row := range rows {
		estimatedCost, err := estimatedCost(row.Cost)
		if err != nil {
			return nil, err
		}
		out = append(out, domain.CompactSessionUsage{
			SessionID: row.SessionID, ProcessedTokens: row.ProcessedTokens,
			Incomplete: row.Incomplete, EstimatedCost: estimatedCost,
		})
	}
	return out, nil
}

func estimatedCost(raw domain.UsageCostAggregate) (*domain.EstimatedCost, error) {
	if err := validateUsageCostAggregate(raw); err != nil {
		return nil, err
	}
	if raw.EventCount == 0 {
		return nil, nil
	}
	coverage := domain.EstimatedCostCoverageComplete
	total := raw.PricedTotalNanos
	if raw.PricedEventCount != raw.EventCount {
		coverage = domain.EstimatedCostCoveragePartial
		var err error
		for _, component := range []struct {
			name  string
			value int64
		}{
			{"input cost", raw.UnpricedKnownInputNanos},
			{"cached input cost", raw.UnpricedKnownCachedInputNanos},
			{"output cost", raw.UnpricedKnownOutputNanos},
		} {
			total, err = checkedUsageAdd(component.name, total, component.value)
			if err != nil {
				return nil, err
			}
		}
		if total == 0 {
			return nil, nil
		}
	}
	providerAttribution, err := estimatedCostProviderAttribution(raw)
	if err != nil {
		return nil, err
	}
	return &domain.EstimatedCost{
		TotalNanos:          total,
		InputNanos:          knownComponent(raw.EventCount, raw.KnownInputCount, raw.KnownInputNanos),
		CachedInputNanos:    knownComponent(raw.EventCount, raw.KnownCachedInputCount, raw.KnownCachedInputNanos),
		OutputNanos:         knownComponent(raw.EventCount, raw.KnownOutputCount, raw.KnownOutputNanos),
		Coverage:            coverage,
		ProviderAttribution: providerAttribution,
	}, nil
}

func estimatedCostProviderAttribution(raw domain.UsageCostAggregate) (domain.EstimatedCostProviderAttribution, error) {
	switch {
	case raw.ObservedCostEventCount > 0 && raw.InferredCostEventCount > 0:
		return domain.EstimatedCostProviderAttributionMixed, nil
	case raw.InferredCostEventCount > 0:
		return domain.EstimatedCostProviderAttributionInferred, nil
	case raw.ObservedCostEventCount > 0:
		return domain.EstimatedCostProviderAttributionObserved, nil
	default:
		return "", fmt.Errorf("usage estimated cost has no provider attribution")
	}
}

func knownComponent(eventCount, knownCount, value int64) *int64 {
	if eventCount == knownCount {
		return &value
	}
	return nil
}

func validateUsageCostAggregate(raw domain.UsageCostAggregate) error {
	values := []struct {
		name  string
		value int64
	}{
		{"event count", raw.EventCount}, {"priced event count", raw.PricedEventCount}, {"priced total cost", raw.PricedTotalNanos},
		{"observed cost event count", raw.ObservedCostEventCount}, {"inferred cost event count", raw.InferredCostEventCount},
		{"known input count", raw.KnownInputCount}, {"known input cost", raw.KnownInputNanos}, {"unpriced known input cost", raw.UnpricedKnownInputNanos},
		{"known cached input count", raw.KnownCachedInputCount}, {"known cached input cost", raw.KnownCachedInputNanos}, {"unpriced known cached input cost", raw.UnpricedKnownCachedInputNanos},
		{"known output count", raw.KnownOutputCount}, {"known output cost", raw.KnownOutputNanos}, {"unpriced known output cost", raw.UnpricedKnownOutputNanos},
	}
	for _, item := range values {
		if item.value < 0 {
			return fmt.Errorf("usage %s must be nonnegative", item.name)
		}
	}
	if raw.PricedEventCount > raw.EventCount || raw.KnownInputCount > raw.EventCount ||
		raw.KnownCachedInputCount > raw.EventCount || raw.KnownOutputCount > raw.EventCount ||
		raw.ObservedCostEventCount > raw.EventCount || raw.InferredCostEventCount > raw.EventCount ||
		raw.InferredCostEventCount > raw.EventCount-raw.ObservedCostEventCount {
		return fmt.Errorf("usage cost coverage count exceeds event count")
	}
	if raw.UnpricedKnownInputNanos > raw.KnownInputNanos ||
		raw.UnpricedKnownCachedInputNanos > raw.KnownCachedInputNanos ||
		raw.UnpricedKnownOutputNanos > raw.KnownOutputNanos {
		return fmt.Errorf("usage unpriced component cost exceeds known component cost")
	}
	return nil
}

func checkedUsageAdd(label string, left, right int64) (int64, error) {
	if left < 0 || right < 0 {
		return 0, fmt.Errorf("usage %s must be nonnegative", label)
	}
	if left > math.MaxInt64-right {
		return 0, fmt.Errorf("usage %s overflows int64", label)
	}
	return left + right, nil
}
