package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	agentregistry "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestSetModelEffortOverridePersistsAndSurvivesRefresh(t *testing.T) {
	now := time.Now().UTC()
	cache := &fakeModelCache{records: map[string]ports.CachedAgentModelCatalog{
		"codex\x00": cachedModelRecord(t, "codex", "", now, false),
	}}
	// The refreshed catalog carries the same custom model, unannotated: the
	// override must reapply to the fresh discovery output.
	discoverer := successfulModelDiscoverer()
	discoverer.catalog.Models = []ports.AgentModelInfo{{ID: "custom-model", Label: "Custom"}}
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("codex", "Codex", nil)}, cache, nil, discoverer)

	if err := svc.SetModelEffortOverride(context.Background(), "codex", "", "custom-model", "high"); err != nil {
		t.Fatal(err)
	}
	record, ok, err := cache.GetAgentModelCatalog(context.Background(), "codex", "")
	if err != nil || !ok {
		t.Fatalf("cache read = (%#v, %v, %v)", record, ok, err)
	}
	var stored ports.AgentModelCatalog
	if err := json.Unmarshal([]byte(record.CatalogJSON), &stored); err != nil {
		t.Fatal(err)
	}
	if got := stored.Metadata["effortOverrides"]; got != `{"custom-model":"high"}` {
		t.Fatalf("persisted overrides = %q, want the JSON override map", got)
	}

	if _, err := svc.RevalidateModels(context.Background(), "codex", ""); err != nil {
		t.Fatal(err)
	}
	record, _, _ = cache.GetAgentModelCatalog(context.Background(), "codex", "")
	if err := json.Unmarshal([]byte(record.CatalogJSON), &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Metadata["effortOverrides"] != `{"custom-model":"high"}` {
		t.Fatalf("override lost across refresh: %q", stored.Metadata["effortOverrides"])
	}
	for _, model := range stored.Models {
		if model.ID != "custom-model" {
			continue
		}
		if len(model.Efforts) != 1 || model.Efforts[0] != "high" {
			t.Fatalf("refreshed model efforts = %v, want [high]", model.Efforts)
		}
		return
	}
	t.Fatal("refreshed catalog lost the overridden model")
}

func TestModelsReadAppliesPersistedOverrideWithoutDiscovery(t *testing.T) {
	now := time.Now().UTC()
	record := cachedModelRecord(t, "codex", "", now, false)
	var catalog ports.AgentModelCatalog
	if err := json.Unmarshal([]byte(record.CatalogJSON), &catalog); err != nil {
		t.Fatal(err)
	}
	catalog.Metadata = map[string]string{"effortOverrides": `{"cached-model":"low"}`}
	data, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	record.CatalogJSON = string(data)
	cache := &fakeModelCache{records: map[string]ports.CachedAgentModelCatalog{"codex\x00": record}}
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("codex", "Codex", nil)}, cache, nil, successfulModelDiscoverer())

	got, err := svc.Models(context.Background(), "codex", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 1 || len(got.Models[0].Efforts) != 1 || got.Models[0].Efforts[0] != "low" {
		t.Fatalf("cached read efforts = %+v, want cached-model annotated with low", got.Models)
	}
}

func TestSetModelEffortOverrideClearsAndValidates(t *testing.T) {
	now := time.Now().UTC()
	cache := &fakeModelCache{records: map[string]ports.CachedAgentModelCatalog{
		"codex\x00": cachedModelRecord(t, "codex", "", now, false),
	}}
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("codex", "Codex", nil)}, cache, nil, successfulModelDiscoverer())
	ctx := context.Background()

	if err := svc.SetModelEffortOverride(ctx, "codex", "", "cached-model", "high"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetModelEffortOverride(ctx, "codex", "", "cached-model", ""); err != nil {
		t.Fatal(err)
	}
	record, _, _ := cache.GetAgentModelCatalog(ctx, "codex", "")
	var stored ports.AgentModelCatalog
	if err := json.Unmarshal([]byte(record.CatalogJSON), &stored); err != nil {
		t.Fatal(err)
	}
	if _, exists := stored.Metadata["effortOverrides"]; exists {
		t.Fatal("cleared override still persisted")
	}
	if err := svc.SetModelEffortOverride(ctx, "codex", "", "  ", "high"); err == nil {
		t.Fatal("empty model id accepted")
	}
	if err := svc.SetModelEffortOverride(ctx, "muse", "", "cached-model", "high"); err == nil {
		t.Fatal("override accepted without a cached catalog")
	}
}
