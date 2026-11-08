package pricing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type modelOverride struct {
	rates   *exactRates
	priceAs string
	version string
}

// PriceOverrideVersionPrefix marks a cost priced at the user's own rates.
const PriceOverrideVersionPrefix = "ao-override:"

var overrideRatePattern = regexp.MustCompile(`^\d{1,9}(\.\d{1,12})?$`)

var perMillion = big.NewRat(1_000_000, 1)

// ValidatePriceOverride checks one override against this snapshot: rates must be decimals and a price-as target must name a row the catalog prices.
func (s *Snapshot) ValidatePriceOverride(override domain.UsagePriceOverride) error {
	if _, err := parseOverride(override); err != nil {
		return err
	}
	if target := strings.TrimSpace(override.PriceAs); target != "" {
		if _, _, ok := s.priceAsRow(target); !ok {
			return fmt.Errorf("no catalog prices %q; write it as provider/model", target)
		}
	}
	return nil
}

func parseOverride(override domain.UsagePriceOverride) (modelOverride, error) {
	model := strings.TrimSpace(override.ModelID)
	if model == "" || len(model) > 256 {
		return modelOverride{}, errors.New("model ID is required")
	}
	if target := strings.TrimSpace(override.PriceAs); target != "" {
		if override.Input != "" || override.Output != "" || override.CacheRead != "" || override.CacheWrite != "" {
			return modelOverride{}, errors.New("set either rates or a model to price as, not both")
		}
		return modelOverride{priceAs: target}, nil
	}
	if override.Input == "" || override.Output == "" {
		return modelOverride{}, errors.New("input and output prices are required")
	}
	rates := exactRates{}
	digest := sha256.New()
	digest.Write([]byte(model))
	for _, field := range []struct {
		value  string
		target **big.Rat
	}{
		{override.Input, &rates.input},
		{override.Output, &rates.output},
		{override.CacheRead, &rates.read},
		{override.CacheWrite, &rates.write},
	} {
		digest.Write([]byte{0})
		if field.value == "" {
			continue
		}
		if !overrideRatePattern.MatchString(field.value) {
			return modelOverride{}, fmt.Errorf("price %q is not a decimal number of dollars", field.value)
		}
		rate, _ := new(big.Rat).SetString(field.value)
		*field.target = rate.Quo(rate, perMillion)
		digest.Write([]byte(rate.RatString()))
	}
	if rates.read == nil {
		rates.read = rates.input
	}
	return modelOverride{rates: &rates, version: PriceOverrideVersionPrefix + "sha256:" + hex.EncodeToString(digest.Sum(nil))}, nil
}

func (s *Snapshot) priceAsRow(target string) (providerID, modelID string, ok bool) {
	if s == nil {
		return "", "", false
	}
	if prefix, rest, found := strings.Cut(target, "/"); found {
		providerID = CanonicalProviderID(prefix)
		if provider, listed := s.providers[providerID]; listed {
			modelID = CanonicalModelID(providerID, rest)
			if _, priced := provider.models[modelID]; priced {
				return providerID, modelID, true
			}
		}
	}
	if providerID = s.ProviderForModel(target); providerID != "" {
		return providerID, CanonicalModelID(providerID, target), true
	}
	return "", "", false
}

// PriceAsModels lists the required providers' models as price-as targets.
func (s *Snapshot) PriceAsModels() []string {
	if s == nil {
		return nil
	}
	var out []string
	for _, providerID := range RequiredProviderIDs {
		for modelID := range s.providers[providerID].models {
			if !strings.Contains(modelID, "#") {
				out = append(out, providerID+"/"+modelID)
			}
		}
	}
	sort.Strings(out)
	return out
}

// SetPriceOverrides publishes the current catalog with these overrides in place of the last set, and returns the stored model IDs whose price changed.
func (m *Manager) SetPriceOverrides(ctx context.Context, overrides []domain.UsagePriceOverride) ([]string, error) {
	release, err := m.fence.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	previous := m.current.Load()
	parsed := make(map[string]modelOverride, len(overrides))
	for _, override := range overrides {
		value, err := parseOverride(override)
		if err != nil {
			return nil, fmt.Errorf("price override for %q: %w", override.ModelID, err)
		}
		parsed[strings.TrimSpace(override.ModelID)] = value
	}
	next := *previous
	next.overrides = parsed
	m.current.Store(&next)
	var changed []string
	for model, value := range parsed {
		if old, ok := previous.overrides[model]; !ok || old.priceAs != value.priceAs || old.version != value.version {
			changed = append(changed, model)
		}
	}
	for model := range previous.overrides {
		if _, ok := parsed[model]; !ok {
			changed = append(changed, model)
		}
	}
	sort.Strings(changed)
	return changed, nil
}
