package dockeragent

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const verifyTimeout = 10 * time.Second

// providerVerifyURLs are each provider's model-list endpoints: listing models
// authenticates the key without running (or billing) a model turn. Tests
// point them at a local server.
var providerVerifyURLs = map[string]string{
	"anthropic": "https://api.anthropic.com/v1/models?limit=1",
	"openai":    "https://api.openai.com/v1/models",
	"google":    "https://generativelanguage.googleapis.com/v1beta/models?pageSize=1",
}

var verifyClient = &http.Client{Timeout: verifyTimeout}

// verifyDefaultProvider checks the key of the provider AO's default config
// launches with (its first model's provider) against that provider only.
// Docker Agent has no offline way to do this: `docker-agent models` is a
// static catalog that accepts any key. The provider's answer decides:
// accepted is authorized, rejected is unauthorized, and anything else
// (offline, rate-limited, provider error) stays configured, unverified.
func verifyDefaultProvider(ctx context.Context, env map[string]string) ports.AgentAuthStatus {
	models := DefaultModels(env)
	if len(models) == 0 {
		// doctor found a key in a source AO cannot read; leave it unverified.
		return ports.AgentAuthStatusConfigured
	}
	provider := models[0].Provider
	endpoint, ok := providerVerifyURLs[provider]
	if !ok {
		return ports.AgentAuthStatusConfigured
	}
	// Docker Agent sends an OpenAI key to OPENAI_BASE_URL when it is set (an
	// OpenAI-compatible gateway such as Z.ai), so the key is checked there.
	if base := lookupKey(env, "OPENAI_BASE_URL"); provider == "openai" && base != "" {
		endpoint = strings.TrimRight(base, "/") + "/models"
	}
	key := providerKey(env, provider)
	if key == "" || !credentialEndpointAllowed(endpoint) {
		return ports.AgentAuthStatusConfigured
	}
	ctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return ports.AgentAuthStatusConfigured
	}
	switch provider {
	case "anthropic":
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2023-06-01")
	case "openai":
		req.Header.Set("Authorization", "Bearer "+key)
	case "google":
		req.Header.Set("x-goog-api-key", key)
	}
	// Never follow a redirect with a key: Go strips Authorization on a
	// cross-origin redirect but not x-api-key or x-goog-api-key. A redirect
	// stays unverified.
	client := *verifyClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return ports.AgentAuthStatusConfigured
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return ports.AgentAuthStatusAuthorized
	// Only 401 rejects the key. A 403 can mean the key may chat but not list
	// models, and unauthorized would block Chat, so it stays unverified.
	case resp.StatusCode == http.StatusUnauthorized:
		return ports.AgentAuthStatusUnauthorized
	default:
		return ports.AgentAuthStatusConfigured
	}
}

// credentialEndpointAllowed reports whether a key may be sent to endpoint:
// HTTPS, or plain HTTP to a loopback host only. OPENAI_BASE_URL is
// user-controlled, so a plain-HTTP gateway keeps the key unverified.
func credentialEndpointAllowed(endpoint string) bool {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || u.Host == "" || u.User != nil {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		switch u.Hostname() {
		case "127.0.0.1", "localhost", "::1":
			return true
		}
	}
	return false
}

// providerKey returns the first key providerModels accepts for provider.
func providerKey(env map[string]string, provider string) string {
	for _, entry := range providerModels {
		if entry.provider != provider {
			continue
		}
		for _, key := range entry.keys {
			if value := lookupKey(env, key); value != "" {
				return value
			}
		}
	}
	return ""
}
