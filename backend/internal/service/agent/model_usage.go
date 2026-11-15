package agent

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

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

func (s *Service) modelUsage(ctx context.Context, agentID string) map[string]time.Time {
	if s.sessions == nil || agentID == "" {
		return nil
	}
	records, err := s.sessions.ListAllSessions(ctx)
	if err != nil {
		s.logger.Debug("model usage lookup failed", "agent", agentID, "error", err)
		return nil
	}
	usage := make(map[string]time.Time)
	for _, record := range records {
		model := record.Metadata.Model
		if model == "" || string(record.Harness) != agentID {
			continue
		}
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
