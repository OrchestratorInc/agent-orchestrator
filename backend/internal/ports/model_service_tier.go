package ports

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

// ModelServiceTier preserves the provider's advertised serving tier.
type ModelServiceTier struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// ServiceTierForSpeed resolves only advertised accelerated tiers. Normal is
// explicit so an earlier accelerated turn cannot keep the thread on that tier.
func ServiceTierForSpeed(tiers []ModelServiceTier, speed domain.SpeedMode) (string, bool) {
	if speed == "" || speed == domain.SpeedModeNormal {
		return "default", true
	}
	for _, tier := range tiers {
		if speed == domain.SpeedModeFast && (tier.ID == "priority" || tier.ID == "fast") ||
			speed == domain.SpeedModeUltrafast && tier.ID == "ultrafast" {
			return tier.ID, true
		}
	}
	return "", false
}
