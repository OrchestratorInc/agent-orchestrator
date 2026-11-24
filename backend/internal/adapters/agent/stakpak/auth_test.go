package stakpak

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

func TestAPICheck(t *testing.T) {
	for out, want := range map[string]apiCheckResult{
		"Active profile: default\nAPI check:      ✓ reachable as user@example.com\n":   apiCheckPassed,
		"API check:      ✗ access token expired (run `stakpak auth login`)\n":          apiCheckFailed,
		"API check:      skipped (no stakpak credential on profile 'default')\n":       apiCheckSkipped,
		"Configured credentials:\n  profile 'default':\n    - zai (api_key)\n":         apiCheckUnknown,
		"No credentials configured.\n\nRun 'stakpak auth login' to add credentials.\n": apiCheckNoCredentials,
		"Ops! something went wrong: Failed to load config\n":                           apiCheckUnknown,
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
`), "default", os.Getenv)
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
		// A key never travels in plaintext to a non-loopback host.
		{"custom key over remote http", provider{Type: "custom", Endpoint: "http://provider.invalid/v1", APIKey: "good"}, probeInconclusive},
		{"anthropic key over remote http", provider{Type: "anthropic", Endpoint: "http://provider.invalid/v1/", APIKey: "good"}, probeInconclusive},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := probeProvider(ctx, server.Client(), tc.p); got != tc.want {
				t.Fatalf("probeProvider = %d, want %d", got, tc.want)
			}
		})
	}
}

// Go keeps x-api-key on a cross-origin redirect, so the probe must not follow
// one: the key would reach the redirect target.
func TestProbeProviderDoesNotFollowRedirects(t *testing.T) {
	leaked := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked <- r.Header.Get("x-api-key") + r.Header.Get("Authorization")
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	for _, p := range []provider{
		{Type: "anthropic", Endpoint: origin.URL + "/v1/", APIKey: "secret"},
		{Type: "custom", Endpoint: origin.URL + "/v1", APIKey: "secret"},
	} {
		if got := probeProvider(context.Background(), origin.Client(), p); got != probeInconclusive {
			t.Fatalf("%s probe through a redirect = %d, want inconclusive", p.Type, got)
		}
	}
	select {
	case header := <-leaked:
		t.Fatalf("redirect target received a request (credential headers %q)", header)
	default:
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

// `stakpak auth login` and TUI onboarding save `auth = { type = "api", key }`
// and clear `api_key`; an OAuth login saves `{ type = "oauth", access, ... }`.
func TestActiveProvidersReadsStakpakAuthTable(t *testing.T) {
	providers, err := activeProviders([]byte(`
[profiles.default.providers.anthropic]
type = "anthropic"
auth = { type = "api", key = "saved-by-login" }

[profiles.default.providers.claude]
type = "anthropic"
auth = { type = "oauth", access = "token", refresh = "r", expires = 1 }
`), "default", os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 2 {
		t.Fatalf("providers = %+v", providers)
	}
	byName := map[string]provider{}
	for _, p := range providers {
		byName[p.Name] = p
	}
	if got := byName["anthropic"]; got.APIKey != "saved-by-login" || got.OAuth {
		t.Fatalf("api auth table = %+v, want its key", got)
	}
	if got := byName["claude"]; !got.OAuth || got.APIKey != "" {
		t.Fatalf("oauth auth table = %+v, want an OAuth login", got)
	}
	// A subscription login cannot be checked without a real request, so it
	// reads as configured rather than signed out.
	if got := probeProvider(context.Background(), http.DefaultClient, byName["claude"]); got != probeInconclusive {
		t.Fatalf("oauth probe = %d, want inconclusive", got)
	}
}

func TestProbeProvidersUsesAuthTableKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("STAKPAK_PROFILE", "")
	if err := os.MkdirAll(filepath.Join(home, ".stakpak"), 0o700); err != nil {
		t.Fatal(err)
	}
	config := "[profiles.default.providers.anthropic]\ntype = \"anthropic\"\napi_endpoint = \"" + server.URL + "/v1/\"\nauth = { type = \"api\", key = \"good\" }\n"
	if err := os.WriteFile(filepath.Join(home, ".stakpak", "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := probeProviders(context.Background(), server.Client()); err != nil || got != ports.AgentAuthStatusAuthorized {
		t.Fatalf("probeProviders = %q, %v; want authorized from the auth table key", got, err)
	}
}

// `stakpak auth status` reads stored credentials only, so it prints "No
// credentials configured." for a keyless provider that runs on
// ANTHROPIC_API_KEY, or for STAKPAK_API_KEY alone.
func TestEnvironmentKeysAreNotSignedOut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "env-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("STAKPAK_PROFILE", "")
	t.Setenv("STAKPAK_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "env-key")
	if err := os.MkdirAll(filepath.Join(home, ".stakpak"), 0o700); err != nil {
		t.Fatal(err)
	}
	config := "[profiles.default.providers.anthropic]\ntype = \"anthropic\"\napi_endpoint = \"" + server.URL + "/v1/\"\n"
	if err := os.WriteFile(filepath.Join(home, ".stakpak", "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := authCheck(context.Background(), server.Client()); err != nil || got != ports.AgentAuthStatusAuthorized {
		t.Fatalf("keyless provider with ANTHROPIC_API_KEY = %q, %v; want authorized", got, err)
	}

	// os.UserHomeDir reads USERPROFILE on Windows and HOME elsewhere.
	empty := t.TempDir()
	t.Setenv("HOME", empty)
	t.Setenv("USERPROFILE", empty)
	if got, err := authCheck(context.Background(), server.Client()); err != nil || got != ports.AgentAuthStatusUnauthorized {
		t.Fatalf("no credentials = %q, %v; want unauthorized", got, err)
	}
	t.Setenv("STAKPAK_API_KEY", "stakpak-key")
	if got, err := authCheck(context.Background(), server.Client()); err != nil || got != ports.AgentAuthStatusConfigured {
		t.Fatalf("STAKPAK_API_KEY only = %q, %v; want configured", got, err)
	}
}

// authCheck is AuthStatus after `auth status` reports no stored credential.
func authCheck(ctx context.Context, client *http.Client) (ports.AgentAuthStatus, error) {
	status, err := probeProviders(ctx, client)
	return withEnvironmentKey(ctx, status, err)
}

// The readiness check passes the environment Stakpak sessions receive. A key
// AO withholds from this harness must read as unset even when the daemon's
// own environment has it.
func TestAuthStatusHonoursWithheldKeys(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "env-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	bin := fakeStakpak(t, `{"sessions":[]}`, "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("STAKPAK_PROFILE", "")
	t.Setenv("ANTHROPIC_API_KEY", "env-key")
	t.Setenv("STAKPAK_API_KEY", "stakpak-key")
	if err := os.MkdirAll(filepath.Join(home, ".stakpak"), 0o700); err != nil {
		t.Fatal(err)
	}
	config := "[profiles.default.providers.anthropic]\ntype = \"anthropic\"\napi_endpoint = \"" + server.URL + "/v1/\"\n"
	if err := os.WriteFile(filepath.Join(home, ".stakpak", "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	if got, err := New().AuthStatus(context.Background()); err != nil || got != ports.AgentAuthStatusAuthorized {
		t.Fatalf("AuthStatus with the daemon's keys = %q, %v; want authorized", got, err)
	}
	withheld := ports.WithAuthEnvironment(context.Background(), map[string]string{"ANTHROPIC_API_KEY": "", "STAKPAK_API_KEY": ""})
	if got, err := New().AuthStatus(withheld); err != nil || got != ports.AgentAuthStatusUnauthorized {
		t.Fatalf("AuthStatus with keys withheld = %q, %v; want unauthorized", got, err)
	}
	if calls := fakeStakpakCalls(t, bin); !slices.Contains(calls, "auth status") {
		t.Fatalf("fake stakpak calls = %q; want the auth status check", calls)
	}
}
