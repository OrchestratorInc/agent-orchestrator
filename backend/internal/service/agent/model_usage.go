package agent

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// withModelUsage stamps each model with the latest activity of a session running
// it, so a picker can open on what this user last ran. The stamp is derived on
// read and never persisted, keeping the cached catalog a record of the provider.
func (s *Service) withModelUsage(ctx context.Context, agentID string, catalog ports.AgentModelCatalog) ports.AgentModelCatalog {
	usage := s.modelUsage(ctx, agentID)
	if len(usage) == 0 {
		return catalog
	}
	models := make([]ports.AgentModelInfo, len(catalog.Models))
	copy(models, catalog.Models)
	for i := range models {
		if at, ok := usage[models[i].ID]; ok {
			models[i].LastUsedAt = &at
		}
	}
	catalog.Models = models
	return catalog
}

// modelUsage maps each session model to its latest activity across every
// project, matching the device-wide catalog it stamps.
func (s *Service) modelUsage(ctx context.Context, agentID string) map[string]time.Time {
	if s.sessions == nil || agentID == "" {
		return nil
	}
	records, err := s.sessions.ListAllSessions(ctx)
	if err != nil {
		// A picker without its recency hint still works.
		s.logger.Debug("model usage lookup failed", "agent", agentID, "error", err)
		return nil
	}
	usage := make(map[string]time.Time)
	for _, record := range records {
		model := record.Metadata.Model
		if model == "" || string(record.Harness) != agentID {
			continue
		}
		// Activity, not UpdatedAt, which renames and pins also advance.
		at := record.CreatedAt
		if record.Activity.LastActivityAt.After(at) {
			at = record.Activity.LastActivityAt
		}
		recordUsage(usage, model, at)
	}
	return usage
}

func recordUsage(usage map[string]time.Time, model string, at time.Time) {
	if existing, ok := usage[model]; !ok || at.After(existing) {
		usage[model] = at
	}
}
