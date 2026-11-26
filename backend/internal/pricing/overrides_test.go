package pricing

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestPriceOverridesPriceTheirModelAndSurviveActivation(t *testing.T) {
	ctx := context.Background()
	manager := NewManager(nil)
	if _, err := manager.Activate(ctx, decodeTestSnapshot(t, testBaseModels("0.1"))); err != nil {
		t.Fatal(err)
	}
	overrides := []domain.UsagePriceOverride{
		{ModelID: "custom-model", Input: "2", Output: "8.50"},
		{ModelID: "proxy-gpt", PriceAs: "gpt-test"},
	}
	changed, err := manager.SetPriceOverrides(ctx, overrides)
	if err != nil || !reflect.DeepEqual(changed, []string{"custom-model", "proxy-gpt"}) {
		t.Fatalf("SetPriceOverrides = %v, %v", changed, err)
	}

	estimate := func(model string) Estimate {
		t.Helper()
		got, err := manager.Snapshot().Estimate(domain.ModelUsageEvent{ModelID: model, Tokens: pricingTokens(1_000_000, 0, 1_000_000, 1_000_000)})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	custom := estimate("custom-model")
	assertCost(t, "custom total", custom.TotalNanos, 10_500_000_000)
	if !strings.HasPrefix(custom.PricingVersion, PriceOverrideVersionPrefix) {
		t.Fatalf("custom version = %q", custom.PricingVersion)
	}
	proxy := estimate("proxy-gpt")
	assertCost(t, "proxy total", proxy.TotalNanos, 200_000_000_000_000)
	if proxy.PricingVersion != manager.Snapshot().ProviderVersion("openai") {
		t.Fatalf("proxy version = %q, want openai's", proxy.PricingVersion)
	}

	if _, err := manager.Activate(ctx, decodeTestSnapshot(t, testBaseModels("0.2"))); err != nil {
		t.Fatal(err)
	}
	assertCost(t, "custom total after activation", estimate("custom-model").TotalNanos, 10_500_000_000)
	assertCost(t, "proxy total after activation", estimate("proxy-gpt").TotalNanos, 400_000_000_000_000)

	changed, err = manager.SetPriceOverrides(ctx, []domain.UsagePriceOverride{{ModelID: "custom-model", Input: "2.0", Output: "8.5"}})
	if err != nil || !reflect.DeepEqual(changed, []string{"proxy-gpt"}) {
		t.Fatalf("second SetPriceOverrides = %v, %v; want only the removed proxy-gpt", changed, err)
	}
	if estimate("proxy-gpt").TotalNanos != nil {
		t.Fatal("a removed override still prices its model")
	}

	for _, bad := range []domain.UsagePriceOverride{
		{ModelID: "x", PriceAs: "no-such-model"},
		{ModelID: "x", Input: "abc", Output: "1"},
		{ModelID: "x", Input: "1"},
		{ModelID: "x", Input: "1", Output: "1", PriceAs: "gpt-test"},
		{ModelID: " ", Input: "1", Output: "1"},
	} {
		if err := manager.Snapshot().ValidatePriceOverride(bad); err == nil {
			t.Errorf("ValidatePriceOverride(%+v) accepted", bad)
		}
	}
}
