package modelcatalog

import (
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestMarkBrowsedProvidersMarksCredentialedProviders(t *testing.T) {
	const agentID = "test-browse-providers"
	browseProviderCredentials[agentID] = func(string, map[string]string) []string { return []string{"zai", "anthropic"} }
	t.Cleanup(func() { delete(browseProviderCredentials, agentID) })
	catalog := ports.AgentModelCatalog{
		ConfiguredProviders: []string{"zai"},
		Models:              []ports.AgentModelInfo{{ID: "glm", Provider: "zai"}, {ID: "gpt", Provider: "openai"}},
	}

	markBrowsedProviders(&catalog, ports.AgentModelDiscoveryRequest{AgentID: agentID, BrowseAll: true})

	if want := []string{"anthropic", "zai"}; !reflect.DeepEqual(catalog.ConfiguredProviders, want) {
		t.Fatalf("configured providers = %v, want %v", catalog.ConfiguredProviders, want)
	}

	// A harness without a registered check is left exactly as discovered.
	untouched := ports.AgentModelCatalog{Models: []ports.AgentModelInfo{{ID: "glm", Provider: "zai"}}}
	markBrowsedProviders(&untouched, ports.AgentModelDiscoveryRequest{AgentID: "unregistered", BrowseAll: true})
	if untouched.ConfiguredProviders != nil {
		t.Fatalf("unregistered catalog = %#v", untouched)
	}
}

func TestMarkBrowsedProvidersGroupsClineModelsByServingProvider(t *testing.T) {
	check := browseProviderCredentials["cline"]
	browseProviderCredentials["cline"] = func(string, map[string]string) []string { return []string{"cline", "zai-coding-plan"} }
	t.Cleanup(func() { browseProviderCredentials["cline"] = check })
	catalog := ports.AgentModelCatalog{Models: []ports.AgentModelInfo{
		{ID: "anthropic/claude-sonnet-5.5", Provider: "anthropic"},
		{ID: "~anthropic/claude-opus-latest", Provider: "~anthropic"},
		{ID: "cline", Label: "Cline Usage-Billing"},
		{ID: "openai-codex", Label: "OpenAI ChatGPT Subscription"},
	}}

	markBrowsedProviders(&catalog, ports.AgentModelDiscoveryRequest{AgentID: "cline", BrowseAll: true})

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
