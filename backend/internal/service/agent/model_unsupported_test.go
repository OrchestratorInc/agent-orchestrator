package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	agentregistry "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// MarkModelUnsupported must record the fact on every cached catalog for the
// agent (the fact belongs to the install, not a project) and leave unrelated
// models and other agents alone.
func TestMarkModelUnsupportedMarksEveryScope(t *testing.T) {
	catalog := ports.AgentModelCatalog{
		AgentID:       "claude-code",
		SelectionMode: ports.ModelSelectionCatalog,
		Models: []ports.AgentModelInfo{
			{ID: "claude-opus-4-1", Label: "Opus"},
			{ID: "claude-sonnet-4-5", Label: "Sonnet"},
		},
		Source: "provider",
	}
	data, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	cache := &fakeModelCache{records: map[string]ports.CachedAgentModelCatalog{
		"claude-code\x00":     {AgentID: "claude-code", ProjectID: "", CatalogJSON: string(data), BinaryVersion: "v1"},
		"claude-code\x00proj": {AgentID: "claude-code", ProjectID: "proj", CatalogJSON: string(data), BinaryVersion: "v1"},
		"codex\x00":           {AgentID: "codex", ProjectID: "", CatalogJSON: string(data), BinaryVersion: "v1"},
	}}
	svc := newService(nil, cache, nil, nil)
	svc.MarkModelUnsupported("claude-code", "claude-opus-4-1", "2.1.228", "2.1.280")

	const wantReason = "Requires Claude Code 2.1.280 or newer (installed 2.1.228)"
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		global := decodeCatalog(t, cache, "claude-code", "")
		project := decodeCatalog(t, cache, "claude-code", "proj")
		globalOpus := findModel(global.Models, "claude-opus-4-1")
		projectOpus := findModel(project.Models, "claude-opus-4-1")
		if globalOpus != nil && globalOpus.Unsupported && projectOpus != nil && projectOpus.Unsupported {
			if globalOpus.UnsupportedReason != wantReason {
				t.Fatalf("reason = %q, want %q", globalOpus.UnsupportedReason, wantReason)
			}
			if m := findModel(global.Models, "claude-sonnet-4-5"); m == nil || m.Unsupported {
				t.Fatalf("unrelated model was affected: %+v", m)
			}
			codex := decodeCatalog(t, cache, "codex", "")
			if m := findModel(codex.Models, "claude-opus-4-1"); m == nil || m.Unsupported {
				t.Fatalf("another agent's catalog was affected: %+v", m)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("MarkModelUnsupported did not persist the marker")
}

// A marker must survive an ordinary catalog refresh (same discovery inputs) but
// clear once the fingerprint changes — which is what happens after an upgrade.
func TestUnsupportedMarkerSurvivesRefreshUntilFingerprintChanges(t *testing.T) {
	cached := ports.AgentModelCatalog{
		AgentID:       "claude-code",
		SelectionMode: ports.ModelSelectionCatalog,
		Models: []ports.AgentModelInfo{{
			ID: "claude-opus-4-1", Label: "Opus",
			Unsupported:       true,
			UnsupportedReason: "Requires Claude Code 2.1.280 or newer (installed 2.1.228)",
		}},
		Source: "provider",
	}
	data, err := json.Marshal(cached)
	if err != nil {
		t.Fatal(err)
	}
	cache := &fakeModelCache{records: map[string]ports.CachedAgentModelCatalog{
		"claude-code\x00": {AgentID: "claude-code", ProjectID: "", CatalogJSON: string(data), BinaryVersion: "v1"},
	}}
	discoverer := &fakeModelDiscoverer{version: "v1", catalog: ports.AgentModelCatalog{
		SelectionMode: ports.ModelSelectionCatalog,
		Models:        []ports.AgentModelInfo{{ID: "claude-opus-4-1", Label: "Opus"}},
		Source:        "provider",
	}}
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("claude-code", "Claude Code", nil)}, cache, nil, discoverer)

	got, err := svc.Models(context.Background(), "claude-code", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if m := findModel(got.Models, "claude-opus-4-1"); m == nil || !m.Unsupported {
		t.Fatalf("marker lost across a same-inputs refresh: %+v", got.Models)
	}

	// A real discovery adapter returns a fresh catalog each call; the fake
	// shares its backing slice, so reset it to model an upgraded binary that
	// no longer reports the marker.
	discoverer.mu.Lock()
	discoverer.version = "v2"
	discoverer.catalog = ports.AgentModelCatalog{
		SelectionMode: ports.ModelSelectionCatalog,
		Models:        []ports.AgentModelInfo{{ID: "claude-opus-4-1", Label: "Opus"}},
		Source:        "provider",
	}
	discoverer.mu.Unlock()

	got, err = svc.Models(context.Background(), "claude-code", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if m := findModel(got.Models, "claude-opus-4-1"); m == nil || m.Unsupported {
		t.Fatalf("marker survived a fingerprint change (upgrade): %+v", got.Models)
	}
}

func decodeCatalog(t *testing.T, cache *fakeModelCache, agentID, projectID string) ports.AgentModelCatalog {
	t.Helper()
	record, ok, err := cache.GetAgentModelCatalog(context.Background(), agentID, projectID)
	if err != nil || !ok {
		t.Fatalf("GetAgentModelCatalog(%s, %q) = (%v, %v)", agentID, projectID, ok, err)
	}
	var catalog ports.AgentModelCatalog
	if err := json.Unmarshal([]byte(record.CatalogJSON), &catalog); err != nil {
		t.Fatal(err)
	}
	return catalog
}

func findModel(models []ports.AgentModelInfo, id string) *ports.AgentModelInfo {
	for i := range models {
		if models[i].ID == id {
			return &models[i]
		}
	}
	return nil
}
