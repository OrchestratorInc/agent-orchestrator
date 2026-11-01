package claudecode

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/pkg/agentcreds"
)

func pinClaudeNow(t *testing.T, now time.Time) {
	t.Helper()
	previous := claudeNow
	claudeNow = func() time.Time { return now }
	t.Cleanup(func() { claudeNow = previous })
}

func storedLoginContext(expiresAt time.Time, renewable bool, baseURL string) claudeProviderContext {
	return claudeProviderContext{
		providerOK: true, provider: agentcreds.ProviderGateway, found: true,
		credential: agentcreds.Credential{
			Kind: agentcreds.KindOAuthToken, Secret: "sk-ant-oat01-stored", Source: "keychain",
			Provider: agentcreds.ProviderGateway, BaseURL: baseURL,
			ExpiresAt: expiresAt, Renewable: renewable,
		},
	}
}

// An access token past its recorded expiry is renewed by Claude Code on its
// next run, so it must not read as signed out — and nothing is sent, because
// the provider can only answer with a 401 that looks like a sign-out.
func TestExpiredRenewableLoginIsConfiguredNotSignedOut(t *testing.T) {
	InvalidateAuthCache()
	t.Cleanup(InvalidateAuthCache)
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	pinClaudeNow(t, now)
	requests := 0
	server := withStubValidator(t, func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"OAuth access token has expired."}}`))
	})

	status, ok := (&Plugin{}).probeResolvedAuthStatus(context.Background(), storedLoginContext(now.Add(-time.Minute), true, server.URL), true)
	if !ok || status != ports.AgentAuthStatusConfigured {
		t.Fatalf("status = %q ok=%v, want configured", status, ok)
	}
	if requests != 0 {
		t.Fatalf("provider requests = %d, want none for a known-expired token", requests)
	}

	_, err := providerModelsFor(context.Background(), storedLoginContext(now.Add(-time.Minute), true, server.URL))
	if !errors.Is(err, ports.ErrAgentModelDiscoveryCredentialExpired) {
		t.Fatalf("model discovery err = %v, want the expired-login classification", err)
	}
	if requests != 0 {
		t.Fatalf("provider requests = %d, want none for a known-expired token", requests)
	}
}

// Without a refresh token Claude Code cannot renew the login, so the provider
// still decides — and a rejection is classified for the login shortcut while
// keeping the provider's own words.
func TestExpiredLoginWithoutRefreshTokenStillProbes(t *testing.T) {
	InvalidateAuthCache()
	t.Cleanup(InvalidateAuthCache)
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	pinClaudeNow(t, now)
	requests := 0
	server := withStubValidator(t, func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"OAuth access token has expired."}}`))
	})

	_, err := providerModelsFor(context.Background(), storedLoginContext(now.Add(-time.Minute), false, server.URL))
	if !errors.Is(err, ports.ErrAgentModelDiscoveryCredentialRejected) {
		t.Fatalf("err = %v, want a rejected-credential classification", err)
	}
	if !strings.Contains(err.Error(), "OAuth access token has expired.") {
		t.Fatalf("err = %q, want the provider's explanation kept", err)
	}
	if requests != 1 {
		t.Fatalf("provider requests = %d, want one probe", requests)
	}
}
