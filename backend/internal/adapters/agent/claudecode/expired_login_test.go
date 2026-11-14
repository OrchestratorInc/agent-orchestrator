package claudecode

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/modelcatalog"
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

func TestModelDiscoveryWarningNamesDiscoveryOnce(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	pinClaudeNow(t, now)
	status := http.StatusInternalServerError
	server := withStubValidator(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"data":[]}`))
	})
	for _, tc := range []struct {
		name     string
		resolved claudeProviderContext
		status   int
		kind     error
		want     string
	}{
		{
			name:     "expired stored login",
			resolved: storedLoginContext(now.Add(-time.Minute), true, server.URL),
			kind:     ports.ErrAgentModelDiscoveryCredentialExpired,
			want:     "claude-code model discovery: the saved Claude Code login token expired; Claude Code renews it the next time it runs",
		},
		{
			name:     "unsupported provider",
			resolved: claudeProviderContext{},
			want:     "claude-code model discovery: configured provider is unsupported",
		},
		{
			name:     "provider detail",
			resolved: storedLoginContext(now.Add(time.Hour), true, server.URL),
			status:   http.StatusInternalServerError,
			want:     "claude-code model discovery: the configured gateway returned HTTP 500",
		},
		{
			name:     "no models",
			resolved: storedLoginContext(now.Add(time.Hour), true, server.URL),
			status:   http.StatusOK,
			want:     "claude-code model discovery: provider reported no Claude models",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			InvalidateAuthCache()
			t.Cleanup(InvalidateAuthCache)
			status = tc.status
			discoverer := modelcatalog.Discoverer{
				ClaudeModels: func(ctx context.Context, _ ports.AgentModelDiscoveryRequest) ([]ports.AgentModelInfo, error) {
					return providerModelsFor(ctx, tc.resolved)
				},
			}
			_, err := discoverer.Discover(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: "claude-code"})
			if err == nil {
				t.Fatal("discovery succeeded, want a failure")
			}
			if got := strings.Count(err.Error(), "model discovery"); got != 1 {
				t.Fatalf("warning %q names model discovery %d times, want once", err, got)
			}
			if got := strings.Count(err.Error(), "claude-code"); got != 1 {
				t.Fatalf("warning %q names claude-code %d times, want once", err, got)
			}
			if !strings.HasPrefix(err.Error(), tc.want) {
				t.Fatalf("warning = %q, want prefix %q", err, tc.want)
			}
			if tc.kind != nil && !errors.Is(err, tc.kind) {
				t.Fatalf("warning %v lost its %v classification", err, tc.kind)
			}
		})
	}
}

func TestExpiredLoginWithLapsedRefreshTokenIsRejectedByProvider(t *testing.T) {
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
	resolved := storedLoginContext(now.Add(-time.Hour), true, server.URL)
	resolved.credential.RefreshExpiresAt = now.Add(-time.Minute)

	status, ok, step := (&Plugin{}).probeResolvedAuthDiagnostics(context.Background(), resolved, true)
	if !ok || status != ports.AgentAuthStatusUnauthorized || step.Code != "provider_rejected" || step.HTTPStatus != http.StatusUnauthorized {
		t.Fatalf("status=%s ok=%v step=%+v", status, ok, step)
	}
	InvalidateAuthCache()
	_, err := providerModelsFor(context.Background(), resolved)
	if !errors.Is(err, ports.ErrAgentModelDiscoveryCredentialRejected) {
		t.Fatalf("discovery error = %v, want ErrAgentModelDiscoveryCredentialRejected", err)
	}
	if requests == 0 {
		t.Fatal("a login whose refresh token lapsed must be sent to the provider")
	}
}
