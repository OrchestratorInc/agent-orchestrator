package usage

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/pricing"
)

type priceOverrideStore interface {
	ListUsagePriceOverrides(context.Context) ([]domain.UsagePriceOverride, error)
	ReplaceUsagePriceOverrides(context.Context, []domain.UsagePriceOverride, time.Time) error
}

const maxPriceOverrides = 500

// PriceOverrides keeps the user's model prices: stored, live in the pricing snapshot, and applied to stored usage.
type PriceOverrides struct {
	mu         sync.Mutex
	store      priceOverrideStore
	manager    *pricing.Manager
	backfiller *CostBackfiller
}

// NewPriceOverrides wires the override store to the live pricing runtime.
func NewPriceOverrides(store priceOverrideStore, manager *pricing.Manager, backfiller *CostBackfiller) *PriceOverrides {
	return &PriceOverrides{store: store, manager: manager, backfiller: backfiller}
}

// Load publishes the stored overrides.
func (p *PriceOverrides) Load(ctx context.Context) error {
	overrides, err := p.store.ListUsagePriceOverrides(ctx)
	if err != nil {
		return err
	}
	_, err = p.manager.SetPriceOverrides(ctx, overrides)
	return err
}

// List returns the saved overrides and the catalog models a model can price as.
func (p *PriceOverrides) List(ctx context.Context) ([]domain.UsagePriceOverride, []string, error) {
	overrides, err := p.store.ListUsagePriceOverrides(ctx)
	if err != nil {
		return nil, nil, err
	}
	return overrides, p.manager.Snapshot().PriceAsModels(), nil
}

// Replace makes overrides the whole set, then reprices the models it changed.
func (p *PriceOverrides) Replace(ctx context.Context, overrides []domain.UsagePriceOverride) error {
	if len(overrides) > maxPriceOverrides {
		return apierr.Invalid("PRICE_OVERRIDES_INVALID", fmt.Sprintf("at most %d model prices", maxPriceOverrides), nil)
	}
	snapshot := p.manager.Snapshot()
	seen := make(map[string]bool, len(overrides))
	clean := make([]domain.UsagePriceOverride, 0, len(overrides))
	for _, override := range overrides {
		override = domain.UsagePriceOverride{
			ModelID: strings.TrimSpace(override.ModelID),
			Input:   strings.TrimSpace(override.Input), Output: strings.TrimSpace(override.Output),
			CacheRead: strings.TrimSpace(override.CacheRead), CacheWrite: strings.TrimSpace(override.CacheWrite),
			PriceAs: strings.TrimSpace(override.PriceAs),
		}
		if err := snapshot.ValidatePriceOverride(override); err != nil {
			return apierr.Invalid("PRICE_OVERRIDES_INVALID", fmt.Sprintf("%s: %v", override.ModelID, err), map[string]any{"modelId": override.ModelID})
		}
		if seen[override.ModelID] {
			return apierr.Invalid("PRICE_OVERRIDES_INVALID", override.ModelID+" is listed twice", map[string]any{"modelId": override.ModelID})
		}
		seen[override.ModelID] = true
		clean = append(clean, override)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.store.ReplaceUsagePriceOverrides(ctx, clean, time.Now()); err != nil {
		return err
	}
	changed, err := p.manager.SetPriceOverrides(ctx, clean)
	if err != nil {
		return err
	}
	return p.backfiller.RepriceModels(ctx, changed)
}
