package modelcatalog

import (
	"context"
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestSeedEffortsAnnotatesKnownGatewayFamilies(t *testing.T) {
	models := []ports.AgentModelInfo{
		{ID: "glm-4.7"},
		{ID: "GLM-5"},
		{ID: "deepseek-v3.2"},
		{ID: "qwen3-max"},
		{ID: "claude-opus-4-5"},
		{ID: "glm-5", Efforts: []string{"minimal", "high"}, DefaultEffort: "high"},
	}
	seeded := SeedEfforts(models)
	if got := seeded[0]; !slices.Equal(got.Efforts, []string{"low", "medium", "high", "xhigh", "max"}) || got.DefaultEffort != "max" {
		t.Fatalf("glm-4.7 = %+v, want full ladder defaulting max", got)
	}
	if !seeded[0].EffortsSeeded {
		t.Fatalf("glm-4.7 EffortsSeeded = false, want true: %+v", seeded[0])
	}
	if seeded[4].EffortsSeeded {
		t.Fatalf("non-gateway model marked seeded: %+v", seeded[4])
	}
	if got := seeded[1]; !slices.Equal(got.Efforts, []string{"low", "medium", "high", "xhigh", "max"}) {
		t.Fatalf("GLM-5 = %+v, want case-insensitive prefix match", got)
	}
	for _, index := range []int{2, 3} {
		if got := seeded[index]; !slices.Equal(got.Efforts, []string{"low", "medium", "high"}) || got.DefaultEffort != "medium" {
			t.Fatalf("%s = %+v, want low/medium/high defaulting medium", seeded[index].ID, got)
		}
	}
	if seeded[4].Efforts != nil || seeded[4].DefaultEffort != "" {
		t.Fatalf("non-gateway model annotated: %+v", seeded[4])
	}
	if !slices.Equal(seeded[5].Efforts, []string{"minimal", "high"}) {
		t.Fatalf("advertised efforts overwritten: %+v", seeded[5])
	}
}

// A gateway whose /v1/models omits capabilities.effort (z.ai probed
// 2026-10-01: only id/created/owned_by come back) must still light up effort
// pickers: the provider-discovered glm model leaves discovery seeded.
func TestDiscoverClaudeCatalogSeedsProviderModels(t *testing.T) {
	discoverer := Discoverer{ClaudeModels: func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.AgentModelInfo, error) {
		return []ports.AgentModelInfo{{ID: "glm-4.7", Label: "GLM-4.7"}, {ID: "claude-opus-4-5"}}, nil
	}}
	catalog, err := discoverer.Discover(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: "claude-code"})
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range catalog.Models {
		switch model.ID {
		case "glm-4.7":
			if !slices.Equal(model.Efforts, []string{"low", "medium", "high", "xhigh", "max"}) || model.DefaultEffort != "max" {
				t.Fatalf("provider glm-4.7 = %+v, want seeded efforts", model)
			}
		case "claude-opus-4-5":
			if model.Efforts != nil {
				t.Fatalf("non-gateway provider model annotated: %+v", model)
			}
		}
	}
}

// Bumping the seed version must invalidate catalogs cached while the gateway
// data was still absent, so the fingerprint folds it in.
func TestClaudeDiscoveryFingerprintTracksEffortSeeds(t *testing.T) {
	settings := map[string]string{}
	fingerprint := func() string {
		return claudeCodeDiscoveryFingerprint(context.Background(), "", settings)
	}
	before := fingerprint()
	previous := effortSeedsVersion
	effortSeedsVersion = "test-next"
	after := fingerprint()
	effortSeedsVersion = previous
	if before == after {
		t.Fatal("fingerprint ignored the effort seed version")
	}
}
