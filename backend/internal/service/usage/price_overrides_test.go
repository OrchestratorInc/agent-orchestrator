package usage

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/pricing"
)

type fakePriceOverrideStore struct{ saved []domain.UsagePriceOverride }

func (s *fakePriceOverrideStore) ListUsagePriceOverrides(context.Context) ([]domain.UsagePriceOverride, error) {
	return s.saved, nil
}

func (s *fakePriceOverrideStore) ReplaceUsagePriceOverrides(_ context.Context, overrides []domain.UsagePriceOverride, _ time.Time) error {
	s.saved = overrides
	return nil
}

func TestPriceOverridesReplaceValidatesStoresAndPublishes(t *testing.T) {
	ctx := context.Background()
	manager := pricing.NewManager(backfillTestSnapshot(t, "0.000001"))
	store := &fakePriceOverrideStore{}
	overrides := NewPriceOverrides(store, manager, NewCostBackfiller(newFakeBackfillStore(0), CostBackfillerConfig{Manager: manager}))

	for name, bad := range map[string][]domain.UsagePriceOverride{
		"duplicate":     {{ModelID: "m", Input: "1", Output: "1"}, {ModelID: " m ", PriceAs: "gpt-test"}},
		"unknown model": {{ModelID: "m", PriceAs: "no-such-model"}},
		"not a number":  {{ModelID: "m", Input: "$1", Output: "1"}},
	} {
		var apiErr *apierr.Error
		if err := overrides.Replace(ctx, bad); !errors.As(err, &apiErr) || apiErr.Kind != apierr.KindInvalid {
			t.Errorf("%s: Replace = %v, want an invalid-request error", name, err)
		}
	}
	if store.saved != nil {
		t.Fatalf("a rejected set was stored: %+v", store.saved)
	}

	if err := overrides.Replace(ctx, []domain.UsagePriceOverride{{ModelID: " proxy ", PriceAs: " openai/gpt-test "}}); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	want := []domain.UsagePriceOverride{{ModelID: "proxy", PriceAs: "openai/gpt-test"}}
	if !reflect.DeepEqual(store.saved, want) {
		t.Fatalf("stored %+v, want trimmed %+v", store.saved, want)
	}
	input := int64(1)
	estimate, err := manager.Snapshot().Estimate(domain.ModelUsageEvent{ModelID: "proxy", Tokens: testUsageMetrics(input, 0, input, 0)})
	if err != nil || estimate.TotalNanos == nil || *estimate.TotalNanos != 1000 {
		t.Fatalf("live estimate = %+v, %v; want gpt-test's 1000 nanos", estimate, err)
	}
}
