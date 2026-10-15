package modelcatalog

import (
	"context"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestMarkConfiguredProvidersMarksCredentialedProviders(t *testing.T) {
	const agentID = "test-browse-providers"
	browseProviderCredentials[agentID] = func(string, map[string]string) []string { return []string{"zai", "anthropic"} }
	t.Cleanup(func() { delete(browseProviderCredentials, agentID) })
	catalog := ports.AgentModelCatalog{
		ConfiguredProviders: []string{"zai"},
		Models:              []ports.AgentModelInfo{{ID: "glm", Provider: "zai"}, {ID: "gpt", Provider: "openai"}},
	}

	markConfiguredProviders(context.Background(), &catalog, ports.AgentModelDiscoveryRequest{AgentID: agentID})

	if want := []string{"anthropic", "zai"}; !reflect.DeepEqual(catalog.ConfiguredProviders, want) {
		t.Fatalf("configured providers = %v, want %v", catalog.ConfiguredProviders, want)
	}

	// A harness without a registered check is left exactly as discovered.
	untouched := ports.AgentModelCatalog{Models: []ports.AgentModelInfo{{ID: "glm", Provider: "zai"}}}
	markConfiguredProviders(context.Background(), &untouched, ports.AgentModelDiscoveryRequest{AgentID: "unregistered"})
	if untouched.ConfiguredProviders != nil {
		t.Fatalf("unregistered catalog = %#v", untouched)
	}
}

func TestMarkConfiguredProvidersGroupsClineModelsByServingProvider(t *testing.T) {
	check := browseProviderCredentials["cline"]
	browseProviderCredentials["cline"] = func(string, map[string]string) []string { return []string{"cline", "zai-coding-plan"} }
	t.Cleanup(func() { browseProviderCredentials["cline"] = check })
	catalog := ports.AgentModelCatalog{Models: []ports.AgentModelInfo{
		{ID: "anthropic/claude-sonnet-5.5", Provider: "anthropic"},
		{ID: "~anthropic/claude-opus-latest", Provider: "~anthropic"},
		{ID: "cline", Label: "Cline Usage-Billing"},
		{ID: "openai-codex", Label: "OpenAI ChatGPT Subscription"},
	}}

	markConfiguredProviders(context.Background(), &catalog, ports.AgentModelDiscoveryRequest{AgentID: "cline"})

	got := make([]string, 0, len(catalog.Models))
	for _, model := range catalog.Models {
		got = append(got, model.Provider)
	}
	if want := []string{"cline", "cline", "cline", "openai-codex"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("model providers = %v, want %v", got, want)
	}
	if want := []string{"cline", "zai-coding-plan"}; !reflect.DeepEqual(catalog.ConfiguredProviders, want) {
		t.Fatalf("configured providers = %v, want %v", catalog.ConfiguredProviders, want)
	}
}
