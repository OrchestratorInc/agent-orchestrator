package stakpak

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestAPICheck(t *testing.T) {
	for out, want := range map[string]apiCheckResult{
		"Active profile: default\nAPI check:      ✓ reachable as user@example.com\n": apiCheckPassed,
		"API check:      ✗ access token expired (run `stakpak auth login`)\n":        apiCheckFailed,
		"API check:      skipped (no stakpak credential on profile 'default')\n":     apiCheckSkipped,
		"Configured credentials:\n  profile 'default':\n    - zai (api_key)\n":       apiCheckUnknown,
		"Ops! something went wrong: Failed to load config\n":                         apiCheckUnknown,
	} {
		if got := apiCheck(out); got != want {
			t.Errorf("apiCheck(%q) = %d, want %d", out, got, want)
		}
	}
}

func TestActiveProvidersMergesAllProfileAndEnvironment(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "env-key")
	providers, err := activeProviders([]byte(`
[profiles.all.providers.openai]
type = "openai"

[profiles.default]
provider = "local"
model = "zai/glm-5.1"

[profiles.default.providers.zai]
type = "custom"
api_endpoint = "https://api.z.ai/api/coding/paas/v4"
api_key = "zai-key"

[profiles.other.providers.anthropic]
type = "anthropic"
api_key = "ignored"

[settings]
auto_append_gitignore = false
`), "default")
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 2 || providers[0].Name != "openai" || providers[0].APIKey != "env-key" ||
		providers[1].Name != "zai" || providers[1].Type != "custom" || providers[1].APIKey != "zai-key" {
		t.Fatalf("providers = %+v", providers)
	}
}

func TestProbeProvider(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		good := r.Header.Get("Authorization") == "Bearer good" || r.Header.Get("x-api-key") == "good"
		switch {
		case !good:
			w.WriteHeader(http.StatusUnauthorized)
		case r.URL.Path == "/v1/models" && r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/anthropic/v1/messages" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusBadRequest) // empty conversation, key accepted
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		p    provider
		want probeVerdict
	}{
		{"custom accepted", provider{Type: "custom", Endpoint: server.URL + "/v1", APIKey: "good"}, probeAccepted},
		{"custom rejected", provider{Type: "custom", Endpoint: server.URL + "/v1", APIKey: "bad"}, probeRejected},
		{"anthropic accepted", provider{Type: "anthropic", Endpoint: server.URL + "/anthropic/v1/", APIKey: "good"}, probeAccepted},
		{"anthropic messages suffix", provider{Type: "anthropic", Endpoint: server.URL + "/anthropic/v1/messages", APIKey: "good"}, probeAccepted},
		{"anthropic rejected", provider{Type: "anthropic", Endpoint: server.URL + "/anthropic/v1/", APIKey: "bad"}, probeRejected},
		{"anthropic without key", provider{Type: "anthropic"}, probeRejected},
		{"server error", provider{Type: "custom", Endpoint: server.URL + "/broken", APIKey: "good"}, probeInconclusive},
		{"unknown type", provider{Type: "amazon-bedrock"}, probeInconclusive},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := probeProvider(ctx, server.Client(), tc.p); got != tc.want {
				t.Fatalf("probeProvider = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestProbeProvidersVerdict(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	write := func(t *testing.T, key string) {
		t.Helper()
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		t.Setenv("STAKPAK_PROFILE", "")
		dir := filepath.Join(home, ".stakpak")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		config := "[profiles.default.providers.mine]\ntype = \"custom\"\napi_endpoint = \"" + server.URL + "\"\napi_key = \"" + key + "\"\n"
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for key, want := range map[string]ports.AgentAuthStatus{"good": ports.AgentAuthStatusAuthorized, "bad": ports.AgentAuthStatusUnauthorized} {
		t.Run(key, func(t *testing.T) {
			write(t, key)
			got, err := probeProviders(context.Background(), server.Client())
			if err != nil || got != want {
				t.Fatalf("probeProviders = %q, %v; want %q", got, err, want)
			}
		})
	}
	t.Run("no config", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("USERPROFILE", t.TempDir())
		if got, err := probeProviders(context.Background(), server.Client()); err != nil || got != ports.AgentAuthStatusUnauthorized {
			t.Fatalf("probeProviders = %q, %v", got, err)
		}
	})
}
