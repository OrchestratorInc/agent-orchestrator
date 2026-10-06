package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func usageSession(project, harness, model string, at time.Time) domain.SessionRecord {
	return domain.SessionRecord{
		ProjectID: domain.ProjectID(project), Harness: domain.AgentHarness(harness),
		Metadata: domain.SessionMetadata{Model: model}, Activity: domain.Activity{LastActivityAt: at}, CreatedAt: at,
	}
}

// stampedModels returns each stamped model ID and its last-used time.
func stampedModels(t *testing.T, svc *Service, project string) map[string]time.Time {
	t.Helper()
	catalog := ports.AgentModelCatalog{Models: []ports.AgentModelInfo{{ID: "fable"}, {ID: "opus"}, {ID: "haiku"}}}
	got := svc.withModelUsage(context.Background(), "claude-code", project, catalog)
	if len(got.Models) != 3 || got.Models[0].ID != "fable" || got.Models[2].ID != "haiku" {
		t.Fatalf("catalog order changed: %+v", got.Models)
	}
	stamped := map[string]time.Time{}
	for _, model := range got.Models {
		if model.LastUsedAt != nil {
			stamped[model.ID] = *model.LastUsedAt
		}
	}
	return stamped
}

func TestWithModelUsageStampsTheLatestSessionOfEachModelRunByThisAgent(t *testing.T) {
	now := time.Now().UTC()
	svc := newService(nil, nil, nil, nil)
	svc.sessions = fakeSessionUsageLookup{records: []domain.SessionRecord{
		usageSession("p1", "claude-code", "opus", now.Add(-2*time.Hour)),
		usageSession("p1", "claude-code", "opus", now.Add(-10*time.Minute)),
		usageSession("p1", "codex", "haiku", now),
	}}

	if got := stampedModels(t, svc, "p1"); len(got) != 1 || !got["opus"].Equal(now.Add(-10*time.Minute)) {
		t.Fatalf("stamps = %v, want only opus at its latest session", got)
	}
}

func TestWithModelUsagePrefersProjectHistoryAndFallsBackToTheAgent(t *testing.T) {
	now := time.Now().UTC()
	svc := newService(nil, nil, nil, nil)
	svc.sessions = fakeSessionUsageLookup{records: []domain.SessionRecord{
		usageSession("p1", "claude-code", "opus", now.Add(-time.Hour)),
		usageSession("p2", "claude-code", "fable", now.Add(-time.Minute)),
	}}

	if got := stampedModels(t, svc, "p1"); len(got) != 1 || got["opus"].IsZero() {
		t.Fatalf("project stamps = %v, want only the project's own model", got)
	}
	if got := stampedModels(t, svc, "p3"); len(got) != 2 {
		t.Fatalf("new project stamps = %v, want the agent-wide history", got)
	}
}

func TestWithModelUsageDegradesToTheCatalogWhenHistoryIsUnavailable(t *testing.T) {
	svc := newService(nil, nil, nil, nil)
	svc.sessions = fakeSessionUsageLookup{err: errors.New("db unavailable")}

	if got := stampedModels(t, svc, "p1"); len(got) != 0 {
		t.Fatalf("stamps = %v, want none", got)
	}
}
