package agent

import (
	"context"
	"testing"
	"time"

	agentregistry "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func usageSession(project, model string, at time.Time) ports.SessionModelUsage {
	return ports.SessionModelUsage{
		ProjectID:      domain.ProjectID(project),
		Model:          model,
		LastActivityAt: at,
		CreatedAt:      at,
	}
}

func claudeCatalog(ids ...string) ports.AgentModelCatalog {
	models := make([]ports.AgentModelInfo, 0, len(ids))
	for _, id := range ids {
		models = append(models, ports.AgentModelInfo{ID: id, Label: id})
	}
	return ports.AgentModelCatalog{AgentID: "claude-code", Models: models}
}

func modelIDs(catalog ports.AgentModelCatalog) []string {
	ids := make([]string, 0, len(catalog.Models))
	for _, model := range catalog.Models {
		ids = append(ids, model.ID)
	}
	return ids
}

func assertIDs(t *testing.T, catalog ports.AgentModelCatalog, want ...string) {
	t.Helper()
	got := modelIDs(catalog)
	if len(got) != len(want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// The models someone actually runs lead the picker, most recent first, and the
// untouched ones keep the catalog's own newest-family-first order behind them.
func TestModelUsageFloatsRecentModelsToTheTop(t *testing.T) {
	now := time.Now().UTC()
	svc := newService(nil, nil, nil, nil)
	svc.sessions = fakeSessionUsageLookup{modelUsage: []ports.SessionModelUsage{
		usageSession("p1", "fable", now.Add(-2*time.Hour)),
		usageSession("p1", "sonnet", now.Add(-10*time.Minute)),
	}}

	got := svc.withModelUsage(context.Background(), "claude-code", "p1", claudeCatalog("fable", "opus", "sonnet", "haiku"))

	assertIDs(t, got, "sonnet", "fable", "opus", "haiku")
	if got.Models[0].LastUsedAt == nil || got.Models[2].LastUsedAt != nil {
		t.Fatalf("stamps = %+v, want only the used models stamped", got.Models)
	}
}

func TestModelUsageQueriesOnlySelectedHarness(t *testing.T) {
	var requested domain.AgentHarness
	svc := newService(nil, nil, nil, nil)
	svc.sessions = fakeSessionUsageLookup{requestedHarness: &requested}

	got := svc.withModelUsage(context.Background(), "claude-code", "p1", claudeCatalog("fable", "sonnet"))

	assertIDs(t, got, "fable", "sonnet")
	if requested != domain.HarnessClaudeCode {
		t.Fatalf("requested harness = %q, want %q", requested, domain.HarnessClaudeCode)
	}
}

// The model someone uses in one repository says little about another, so a
// project's own history wins where it exists.
func TestModelUsagePrefersProjectHistory(t *testing.T) {
	now := time.Now().UTC()
	svc := newService(nil, nil, nil, nil)
	svc.sessions = fakeSessionUsageLookup{modelUsage: []ports.SessionModelUsage{
		usageSession("other", "sonnet", now),
		usageSession("p1", "opus", now.Add(-time.Hour)),
	}}

	got := svc.withModelUsage(context.Background(), "claude-code", "p1", claudeCatalog("fable", "sonnet", "opus"))

	assertIDs(t, got, "opus", "fable", "sonnet")
}

// A project with no history of its own inherits the agent-wide answer, so the
// first task in a new project still opens on the model the user works with.
func TestModelUsageFallsBackToAgentWideHistory(t *testing.T) {
	now := time.Now().UTC()
	svc := newService(nil, nil, nil, nil)
	svc.sessions = fakeSessionUsageLookup{modelUsage: []ports.SessionModelUsage{
		usageSession("other", "sonnet", now),
	}}

	got := svc.withModelUsage(context.Background(), "claude-code", "fresh", claudeCatalog("fable", "sonnet"))

	assertIDs(t, got, "sonnet", "fable")
}

func TestModelUsageUsesLaterOfActivityAndCreation(t *testing.T) {
	now := time.Now().UTC()
	createdLater := usageSession("p1", "opus", now.Add(-48*time.Hour))
	createdLater.CreatedAt = now
	activeLater := usageSession("p1", "sonnet", now.Add(-24*time.Hour))
	activeLater.LastActivityAt = now.Add(-time.Hour)
	svc := newService(nil, nil, nil, nil)
	svc.sessions = fakeSessionUsageLookup{modelUsage: []ports.SessionModelUsage{
		createdLater,
		activeLater,
	}}

	got := svc.withModelUsage(context.Background(), "claude-code", "p1", claudeCatalog("sonnet", "opus"))

	assertIDs(t, got, "opus", "sonnet")
}

// Losing the recency hint must not empty or reorder the picker.
func TestModelUsageDegradesToCatalogOrder(t *testing.T) {
	svc := newService(nil, nil, nil, nil)
	svc.sessions = fakeSessionUsageLookup{err: context.DeadlineExceeded}

	got := svc.withModelUsage(context.Background(), "claude-code", "p1", claudeCatalog("fable", "opus"))

	assertIDs(t, got, "fable", "opus")
}

func TestCatalogReadPathsApplyModelUsage(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		name string
		load func(*Service) (ports.AgentModelCatalog, error)
	}{
		{
			name: "Models",
			load: func(svc *Service) (ports.AgentModelCatalog, error) {
				return svc.Models(context.Background(), "claude-code", "p1", true)
			},
		},
		{
			name: "RevalidateModels",
			load: func(svc *Service) (ports.AgentModelCatalog, error) {
				return svc.RevalidateModels(context.Background(), "claude-code", "p1")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			discoverer := &fakeModelDiscoverer{catalog: claudeCatalog("fable", "sonnet")}
			svc := newService(
				[]agentregistry.HarnessAgent{harnessAgent("claude-code", "Claude Code", nil)},
				&fakeModelCache{}, nil, discoverer,
			)
			svc.sessions = fakeSessionUsageLookup{modelUsage: []ports.SessionModelUsage{
				usageSession("p1", "sonnet", now),
			}}

			got, err := tc.load(svc)
			if err != nil {
				t.Fatal(err)
			}
			assertIDs(t, got, "sonnet", "fable")
			if got.Models[0].LastUsedAt == nil {
				t.Fatal("most recently used model was not stamped")
			}
		})
	}
}
