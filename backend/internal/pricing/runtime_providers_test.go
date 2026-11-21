package pricing

import (
	"strings"
	"testing"
)

func providerManifest(ids ...string) catalogManifest {
	manifest := catalogManifest{
		SchemaVersion: runtimeSchemaVersion,
		Source: catalogManifestSource{
			Repository: "BerriAI/litellm",
			Revision:   strings.Repeat("a", 40),
			Path:       "model_prices_and_context_window.json",
		},
	}
	digest := strings.Repeat("b", 64)
	for _, id := range ids {
		manifest.Providers = append(manifest.Providers, catalogProviderRef{
			ProviderID: id, SHA256: digest, ModelCount: 1,
			Path:    "providers/" + id + "/" + digest + ".json",
			Version: "ao-catalog:" + id + ":sha256:" + digest,
		})
	}
	return manifest
}

func TestRuntimeManifestAcceptsAnyProviderSetWithTheRequiredOnes(t *testing.T) {
	if err := validateRuntimeManifest(providerManifest("anthropic", "minimax", "openai", "vertex_ai", "zai")); err != nil {
		t.Fatalf("wide manifest: %v", err)
	}
	if err := validateRuntimeManifest(providerManifest("anthropic", "openai", "zai")); err != nil {
		t.Fatalf("required-only manifest: %v", err)
	}
	for name, ids := range map[string][]string{
		"missing required": {"anthropic", "minimax", "openai"},
		"out of order":     {"anthropic", "zai", "openai"},
		"duplicate":        {"anthropic", "openai", "openai", "zai"},
		"unsafe id":        {"../x", "anthropic", "openai", "zai"},
	} {
		if err := validateRuntimeManifest(providerManifest(ids...)); err == nil {
			t.Errorf("%s manifest %v was accepted", name, ids)
		}
	}
}

func TestCanonicalProviderIDMapsHarnessSpellings(t *testing.T) {
	for raw, want := range map[string]string{
		"Z.AI": "zai", "google": "gemini", "google-vertex": "vertex_ai", "amazon-bedrock": "bedrock",
		"github-copilot": "github_copilot", "moonshotai": "moonshot", "minimax": "minimax",
		"aws_bedrock": "bedrock", "gcp_vertex_ai": "vertex_ai",
		"minimax-cn": "minimax-cn", "zai-coding-cn": "zai-coding-cn",
	} {
		if got := CanonicalProviderID(raw); got != want {
			t.Errorf("CanonicalProviderID(%q) = %q, want %q", raw, got, want)
		}
	}
}
