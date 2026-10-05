package agentcreds

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUnsupportedClaudeProvidersStayUnknownWithoutModels(t *testing.T) {
	for _, provider := range []Provider{ProviderBedrock, ProviderVertex, ProviderFoundry} {
		t.Run(string(provider), func(t *testing.T) {
			result := New(nil).ValidateResolvedLocal(context.Background(), provider, Credential{}, false, ResolveOptions{})
			if result.State != StateUnknown || result.Provider != provider || len(result.Models) != 0 {
				t.Fatalf("result = %+v, want an unverified provider with no discovered models", result)
			}
			if !strings.Contains(result.Detail, "not supported") {
				t.Fatalf("detail = %q, want explicit unsupported-validation guidance", result.Detail)
			}
		})
	}
}

func TestValidateLocalDowngradesResolverBugsToUnknown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"x-api-key header is required"}}`))
	}))
	defer server.Close()

	validator := New(server.Client())
	result := validator.ValidateLocal(context.Background(), "gateway", ResolveOptions{Env: envFrom(map[string]string{
		"ANTHROPIC_API_KEY": "present-but-somehow-not-sent", "ANTHROPIC_BASE_URL": server.URL,
	})})
	if result.State != StateUnknown {
		t.Fatalf("state = %q, want unknown", result.State)
	}
}

func TestValidateLocalKeepsGenuineRejections(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"API key is invalid."}}`))
	}))
	defer server.Close()

	validator := New(server.Client())
	result := validator.ValidateLocal(context.Background(), "gateway", ResolveOptions{Env: envFrom(map[string]string{
		"ANTHROPIC_API_KEY": "sk-ant-revoked", "ANTHROPIC_BASE_URL": server.URL,
	})})
	if result.State != StateInvalid {
		t.Fatalf("state = %q (%s), want invalid", result.State, result.Detail)
	}
	if result.Source != "ANTHROPIC_API_KEY" {
		t.Fatalf("source = %q, want the env var that supplied the credential", result.Source)
	}
}

// An expired OAuth access token (a claude.ai subscription whose short-lived
// access token aged out but whose refresh token is still good) must not be
// reported as a signed-out user. The probe is inconclusive, not a rejection, so
// the auth ladder can defer to the CLI and render "configured" rather than
// "signed out".
func TestValidateResolvedLocalTreatsExpiredOAuthAsRefreshable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"OAuth access token has expired. Re-authenticate to continue."}}`))
	}))
	defer server.Close()

	cred := Credential{Kind: KindOAuthToken, Secret: "sk-ant-oat01-stale", Source: "keychain", Provider: ProviderFirstParty, BaseURL: server.URL}
	result := New(server.Client()).ValidateResolvedLocal(context.Background(), ProviderFirstParty, cred, true, ResolveOptions{})
	if result.State != StateUnknown {
		t.Fatalf("state = %q (%s), want unknown — an expired access token is refreshable, not proof of sign-out", result.State, result.Detail)
	}
}

// A revoked OAuth token still returns a decisive rejection: the user really
// does need to sign in again, and the expiry downgrade must not mask that.
func TestValidateResolvedLocalKeepsRevokedOAuthInvalid(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"OAuth access token is invalid."}}`))
	}))
	defer server.Close()

	cred := Credential{Kind: KindOAuthToken, Secret: "sk-ant-oat01-revoked", Source: "keychain", Provider: ProviderFirstParty, BaseURL: server.URL}
	result := New(server.Client()).ValidateResolvedLocal(context.Background(), ProviderFirstParty, cred, true, ResolveOptions{})
	if result.State != StateInvalid {
		t.Fatalf("state = %q (%s), want invalid — a revoked token is a genuine rejection", result.State, result.Detail)
	}
}

func TestIsRefreshableOAuthExpiry(t *testing.T) {
	refreshable := []string{
		"api.anthropic.com rejected the credential: OAuth access token has expired. Re-authenticate to continue.",
		"the oauth token expired",
	}
	for _, detail := range refreshable {
		if !IsRefreshableOAuthExpiry(detail) {
			t.Fatalf("IsRefreshableOAuthExpiry(%q) = false, want true", detail)
		}
	}
	decisive := []string{
		"api.anthropic.com rejected the credential: OAuth access token is invalid.",
		"api.anthropic.com rejected the credential: API key is invalid.",
		"",
	}
	for _, detail := range decisive {
		if IsRefreshableOAuthExpiry(detail) {
			t.Fatalf("IsRefreshableOAuthExpiry(%q) = true, want false — this is a decisive rejection", detail)
		}
	}
}
