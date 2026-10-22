package openjarvis

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// providerModelsEndpoints are key-authenticated, token-free listings that
// reject a bad key with 401/403. The Codex token has no such endpoint, so it
// stays configured.
var providerModelsEndpoints = map[string]string{
	"OPENAI_API_KEY":     "https://api.openai.com/v1/models",
	"ANTHROPIC_API_KEY":  "https://api.anthropic.com/v1/models",
	"GEMINI_API_KEY":     "https://generativelanguage.googleapis.com/v1beta/models",
	"GOOGLE_API_KEY":     "https://generativelanguage.googleapis.com/v1beta/models",
	"OPENROUTER_API_KEY": "https://openrouter.ai/api/v1/key",
	"MINIMAX_API_KEY":    "https://api.minimax.io/v1/models",
	"DEEPSEEK_API_KEY":   "https://api.deepseek.com/models",
}

var authHTTPClient = &http.Client{Timeout: 10 * time.Second}

// providerProbe is the provider round-trip behind AuthStatus; tests replace it.
var providerProbe = probeProviderKey

// AuthStatus checks the key OpenJarvis will use for its default model against
// that provider's API. Only a passed round-trip reports authorized; a rejected
// key, or a model whose provider key is missing, reports unauthorized; an
// offline or inconclusive probe keeps the presence result, configured.
func (*Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	keyEnv, routed := modelProviderKey(nativeDefaultModel())
	if !routed {
		keyEnv = cloudProviderKey(nil)
	}
	if keyEnv == "" {
		if routed {
			return ports.AgentAuthStatusUnauthorized, nil
		}
		return ports.AgentAuthStatusUnknown, nil
	}
	ports.RecordAuthEvidence(ctx, domain.EnvironmentSource(keyEnv).WithMethod(domain.AgentAccountMethodAPIKey))
	return providerProbe(ctx, keyEnv, strings.TrimSpace(os.Getenv(keyEnv))), nil
}

// modelProviderKey mirrors the native cloud engine's model routing. routed
// reports that the model belongs to a cloud provider; keyEnv is the set
// variable for it, or empty when that provider's key is missing.
func modelProviderKey(model string) (keyEnv string, routed bool) {
	m := strings.ToLower(model)
	var candidates []string
	switch {
	case strings.HasPrefix(m, "openrouter/"):
		candidates = []string{"OPENROUTER_API_KEY"}
	case strings.HasPrefix(m, "codex/"):
		candidates = []string{"OPENAI_CODEX_API_KEY"}
	case strings.HasPrefix(m, "minimax"):
		candidates = []string{"MINIMAX_API_KEY"}
	case strings.HasPrefix(m, "deepseek"):
		candidates = []string{"DEEPSEEK_API_KEY"}
	case strings.Contains(m, "claude"):
		candidates = []string{"ANTHROPIC_API_KEY"}
	case strings.Contains(m, "gemini"):
		candidates = []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"}
	case strings.HasPrefix(m, "gpt-") || strings.HasPrefix(m, "chatgpt-") || strings.HasPrefix(m, "o1") || strings.HasPrefix(m, "o3") || strings.HasPrefix(m, "o4"):
		candidates = []string{"OPENAI_API_KEY"}
	default:
		return "", false
	}
	for _, env := range candidates {
		if strings.TrimSpace(os.Getenv(env)) != "" {
			return env, true
		}
	}
	return "", true
}

// nativeDefaultModel reads [intelligence].default_model from the user's
// native configuration; any read or parse failure is no model.
func nativeDefaultModel() string {
	path := os.Getenv("OPENJARVIS_CONFIG")
	if path == "" {
		path = filepath.Join(nativeHome(nil), "config.toml")
	}
	path, err := resolveNativePath(path, "", nil)
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var config struct {
		Intelligence struct {
			DefaultModel string `toml:"default_model"`
		} `toml:"intelligence"`
	}
	if toml.Unmarshal(b, &config) != nil {
		return ""
	}
	return strings.TrimSpace(config.Intelligence.DefaultModel)
}

// probeProviderKey lists the provider's models with key. The OpenAI client
// honours OPENAI_BASE_URL, so an OpenAI key is checked where it is sent.
func probeProviderKey(ctx context.Context, keyEnv, key string) ports.AgentAuthStatus {
	endpoint := providerModelsEndpoints[keyEnv]
	if base := strings.TrimRight(os.Getenv("OPENAI_BASE_URL"), "/"); keyEnv == "OPENAI_API_KEY" && base != "" {
		endpoint = base + "/models"
	}
	if endpoint == "" || key == "" {
		return ports.AgentAuthStatusConfigured
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return ports.AgentAuthStatusConfigured
	}
	switch keyEnv {
	case "ANTHROPIC_API_KEY":
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2023-06-01")
	case "GEMINI_API_KEY", "GOOGLE_API_KEY":
		req.Header.Set("x-goog-api-key", key)
	default:
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := authHTTPClient.Do(req)
	if err != nil {
		return ports.AgentAuthStatusConfigured
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return ports.AgentAuthStatusAuthorized
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return ports.AgentAuthStatusUnauthorized
	default:
		return ports.AgentAuthStatusConfigured
	}
}
