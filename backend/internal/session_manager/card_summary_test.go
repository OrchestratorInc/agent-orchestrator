package sessionmanager

import (
	"context"
	"math/big"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/pricing"
)

type cardSummaryCatalog struct{ models []ports.AgentModelInfo }

func (c cardSummaryCatalog) Models(context.Context, string, string, bool) (ports.AgentModelCatalog, error) {
	return ports.AgentModelCatalog{Models: c.models}, nil
}

func cardSummaryRates(prices map[string][3]int64) func(string) (pricing.TokenRates, bool) {
	providers := map[int64]string{1: "anthropic", 2: "openai"}
	return func(id string) (pricing.TokenRates, bool) {
		p, ok := prices[id]
		if !ok {
			return pricing.TokenRates{}, false
		}
		return pricing.TokenRates{ProviderID: providers[p[0]], Input: big.NewRat(p[1], 1), Output: big.NewRat(p[2], 1)}, true
	}
}

func TestCardSummaryModelPicksTheCheapestModelOfTheWorkersProvider(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	models := []ports.AgentModelInfo{
		{ID: "opus", Efforts: []string{"low", "high"}},
		{ID: "claude-opus", Efforts: []string{"low", "high"}, IsDefault: true},
		{ID: "claude-sonnet", Efforts: []string{"medium", "high"}},
		{ID: "claude-haiku"},
		{ID: "claude-tiny-new", RequiresVersion: "9.0.0"},
		{ID: "gpt-nano"},
	}
	prices := map[string][3]int64{
		"claude-opus": {1, 15, 75}, "claude-sonnet": {1, 3, 15}, "claude-haiku": {1, 1, 5},
		"claude-tiny-new": {1, 0, 1}, "gpt-nano": {2, 0, 1},
	}
	m := &Manager{modelCatalog: cardSummaryCatalog{models: models}}
	m.SetModelRates(cardSummaryRates(prices))
	pick := func(model string) (string, string) {
		return m.cardSummaryModel(ctx, domain.SessionRecord{Harness: domain.HarnessClaudeCode, Metadata: domain.SessionMetadata{Model: model}})
	}

	// The OpenAI model is cheaper but bills another provider, and the newest
	// one needs a newer CLI; neither is picked.
	for _, worker := range []string{"claude-opus", ""} {
		if model, effort := pick(worker); model != "claude-haiku" || effort != "" {
			t.Fatalf("worker %q: picked %q at %q, want claude-haiku at the default effort", worker, model, effort)
		}
	}

	// A worker model with no price and a catalog that spans providers keeps the
	// worker's own model, at its lowest effort.
	if model, effort := pick("opus"); model != "opus" || effort != "low" {
		t.Fatalf("unpriced worker over two providers: picked %q at %q, want opus at low", model, effort)
	}
	// With one provider in the catalog, an unpriced worker model still gets its
	// cheapest model.
	single := &Manager{modelCatalog: cardSummaryCatalog{models: models[:5]}}
	single.SetModelRates(cardSummaryRates(prices))
	if model, _ := single.cardSummaryModel(ctx, domain.SessionRecord{Metadata: domain.SessionMetadata{Model: "opus"}}); model != "claude-haiku" {
		t.Fatalf("unpriced worker over one provider: picked %q, want claude-haiku", model)
	}

	m.SetModelRates(nil)
	if model, effort := pick("claude-sonnet"); model != "claude-sonnet" || effort != "medium" {
		t.Fatalf("without prices: picked %q at %q, want the worker's model at its lowest effort", model, effort)
	}

	if model, effort := (&Manager{}).cardSummaryModel(ctx, domain.SessionRecord{Metadata: domain.SessionMetadata{Model: "claude-opus"}}); model != "claude-opus" || effort != "" {
		t.Fatalf("without a catalog: picked %q at %q, want the worker's model", model, effort)
	}
}
