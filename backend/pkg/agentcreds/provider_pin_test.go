package agentcreds

import "testing"

func TestProviderPinEnvEmptyPin(t *testing.T) {
	if got := ProviderPinEnv("", "proj", nil); got != nil {
		t.Fatalf("ProviderPinEnv(\"\") = %v, want nil", got)
	}
}

func TestProviderPinEnvDirectShadowsProviderKeys(t *testing.T) {
	got := ProviderPinEnv(ProviderPinDirect, "proj", []GatewayEntry{{Scope: GatewayScopeApp, BaseURL: "https://app.example", Token: "tok"}})
	for _, key := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_MODEL"} {
		value, ok := got[key]
		if !ok || value != "" {
			t.Fatalf("direct pin %s = (%q,%v), want an empty shadow", key, value, ok)
		}
	}
}

func TestProviderPinEnvMatchesConfiguredEntry(t *testing.T) {
	entries := []GatewayEntry{{Scope: GatewayScopeApp, BaseURL: "https://app.example", Token: "app-token", Model: "gateway-model"}}
	got := ProviderPinEnv("https://app.example", "proj", entries)
	if got["ANTHROPIC_BASE_URL"] != "https://app.example" || got["ANTHROPIC_AUTH_TOKEN"] != "app-token" || got["ANTHROPIC_MODEL"] != "gateway-model" {
		t.Fatalf("app-scope pin = %v", got)
	}
}

// Keys the matched entry does not set must be omitted, not emptied: the pin
// overlay runs after the project env, and an empty value would clear a
// project-configured model or credential the entry never had.
func TestProviderPinEnvOmitsUnsetEntryKeys(t *testing.T) {
	entries := []GatewayEntry{{Scope: GatewayScopeApp, BaseURL: "https://bare.example"}}
	got := ProviderPinEnv("https://bare.example", "proj", entries)
	if got["ANTHROPIC_BASE_URL"] != "https://bare.example" {
		t.Fatalf("pin = %v, want the base URL set", got)
	}
	for _, key := range []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_MODEL", "ANTHROPIC_SMALL_FAST_MODEL"} {
		if value, ok := got[key]; ok {
			t.Fatalf("pin carries %s=%q, want the key omitted", key, value)
		}
	}
}

func TestProviderPinEnvProjectScopeWins(t *testing.T) {
	entries := []GatewayEntry{
		{Scope: GatewayScopeApp, BaseURL: "https://same.example", Token: "app-token"},
		{Scope: GatewayScopeProject, ProjectID: "proj", BaseURL: "https://same.example", Token: "project-token"},
	}
	got := ProviderPinEnv("https://same.example", "proj", entries)
	if got["ANTHROPIC_AUTH_TOKEN"] != "project-token" {
		t.Fatalf("pin token = %q, want the project scope to win", got["ANTHROPIC_AUTH_TOKEN"])
	}
}

func TestProviderPinEnvUnknownEntryDegradesToDefault(t *testing.T) {
	entries := []GatewayEntry{{Scope: GatewayScopeApp, BaseURL: "https://other.example", Token: "tok"}}
	if got := ProviderPinEnv("https://gone.example", "proj", entries); got != nil {
		t.Fatalf("unknown pin = %v, want nil", got)
	}
}

func TestDefaultGatewayEnvPrefersProjectScope(t *testing.T) {
	entries := []GatewayEntry{
		{Scope: GatewayScopeApp, BaseURL: "https://app.example", Token: "app-token", Model: "app-model"},
		{Scope: GatewayScopeProject, ProjectID: "proj", BaseURL: "https://project.example", Token: "project-token"},
	}
	got := DefaultGatewayEnv("proj", entries)
	if got["ANTHROPIC_BASE_URL"] != "https://project.example" || got["ANTHROPIC_AUTH_TOKEN"] != "project-token" || got["ANTHROPIC_MODEL"] != "" {
		t.Fatalf("default env = %v, want the project entry without its unset keys", got)
	}
	if got := DefaultGatewayEnv("other", entries); got["ANTHROPIC_BASE_URL"] != "https://app.example" {
		t.Fatalf("other-project default = %v, want the app entry", got)
	}
	if got := DefaultGatewayEnv("proj", nil); got != nil {
		t.Fatalf("no entries = %v, want nil", got)
	}
}

func TestProviderLaunchEnvPinWinsOverDefault(t *testing.T) {
	entries := []GatewayEntry{{Scope: GatewayScopeApp, BaseURL: "https://app.example", Token: "app-token"}}
	if got := ProviderLaunchEnv("direct", "proj", entries); got["ANTHROPIC_BASE_URL"] != "" || got["ANTHROPIC_AUTH_TOKEN"] != "" {
		t.Fatalf("direct launch env = %v, want empty shadows", got)
	}
	if got := ProviderLaunchEnv("", "proj", entries); got["ANTHROPIC_BASE_URL"] != "https://app.example" {
		t.Fatalf("pinless launch env = %v, want the default entry", got)
	}
}
