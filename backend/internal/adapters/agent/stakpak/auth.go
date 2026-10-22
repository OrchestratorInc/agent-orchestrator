package stakpak

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

var _ ports.AgentAuthChecker = (*Plugin)(nil)

// AuthStatus verifies Stakpak's credentials with a provider round-trip.
//
// `stakpak auth status` checks a Stakpak API key against the Stakpak API
// itself ("API check: ✓ reachable as …"). With bring-your-own-key profiles it
// skips that check, so AO reads the active profile's providers from
// ~/.stakpak/config.toml and asks each one whether it accepts its key, without
// spending tokens: a model list for OpenAI-compatible endpoints, and an empty
// Messages request (rejected as invalid only after the key is accepted) for
// Anthropic-compatible ones.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		if errors.Is(err, ports.ErrAgentBinaryNotFound) {
			return ports.AgentAuthStatusUnknown, nil
		}
		return ports.AgentAuthStatusUnknown, err
	}
	statusCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, runErr := process.CommandContext(statusCtx, binary, "auth", "status").CombinedOutput()
	switch apiCheck(string(out)) {
	case apiCheckPassed:
		ports.RecordAuthEvidence(ctx, authprobe.FileSource(configPath()).WithMethod(domain.AgentAccountMethodAPIKey))
		return ports.AgentAuthStatusAuthorized, nil
	case apiCheckFailed:
		return ports.AgentAuthStatusUnauthorized, nil
	case apiCheckSkipped:
		return probeProviders(ctx, http.DefaultClient)
	}
	if runErr != nil {
		return ports.AgentAuthStatusUnknown, fmt.Errorf("stakpak auth status: %w", runErr)
	}
	return ports.AgentAuthStatusUnknown, nil
}

type apiCheckResult int

const (
	apiCheckUnknown apiCheckResult = iota
	apiCheckPassed
	apiCheckFailed
	apiCheckSkipped
)

// apiCheck reads the "API check:" row of `stakpak auth status`.
func apiCheck(out string) apiCheckResult {
	for _, line := range strings.Split(out, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "API check:")
		if !ok {
			continue
		}
		rest = strings.TrimSpace(rest)
		switch {
		case strings.HasPrefix(rest, "✓"):
			return apiCheckPassed
		case strings.HasPrefix(rest, "✗"):
			return apiCheckFailed
		case strings.HasPrefix(rest, "skipped"):
			return apiCheckSkipped
		}
	}
	return apiCheckUnknown
}

// configPath is Stakpak's fixed config location; it has no override variable.
func configPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".stakpak", "config.toml")
}

// provider is one configured LLM provider of the active profile.
type provider struct {
	Name     string
	Type     string `toml:"type"`
	Endpoint string `toml:"api_endpoint"`
	APIKey   string `toml:"api_key"`
}

type stakpakConfig struct {
	Profiles map[string]struct {
		Providers map[string]provider `toml:"providers"`
	} `toml:"profiles"`
}

// activeProviders merges the special "all" profile's providers under the
// active profile's (STAKPAK_PROFILE, else "default"), as Stakpak does.
func activeProviders(data []byte, profile string) ([]provider, error) {
	var config stakpakConfig
	if err := toml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parse stakpak config: %w", err)
	}
	merged := map[string]provider{}
	for _, name := range []string{"all", profile} {
		for key, value := range config.Profiles[name].Providers {
			value.Name = key
			merged[key] = value
		}
	}
	out := make([]provider, 0, len(merged))
	for _, value := range merged {
		if value.Type == "" {
			value.Type = value.Name
		}
		if value.APIKey == "" {
			value.APIKey = os.Getenv(providerKeyEnv[value.Type])
		}
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// providerKeyEnv is Stakpak's environment fallback for built-in providers.
var providerKeyEnv = map[string]string{
	"anthropic": "ANTHROPIC_API_KEY",
	"openai":    "OPENAI_API_KEY",
	"gemini":    "GEMINI_API_KEY",
}

type probeVerdict int

const (
	probeInconclusive probeVerdict = iota
	probeAccepted
	probeRejected
)

func probeProviders(ctx context.Context, client *http.Client) (ports.AgentAuthStatus, error) {
	path := configPath()
	data, err := os.ReadFile(path) //nolint:gosec // Stakpak's own config file
	if errors.Is(err, os.ErrNotExist) {
		return ports.AgentAuthStatusUnauthorized, nil
	}
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	profile := strings.TrimSpace(os.Getenv("STAKPAK_PROFILE"))
	if profile == "" {
		profile = "default"
	}
	providers, err := activeProviders(data, profile)
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	if len(providers) == 0 {
		return ports.AgentAuthStatusUnauthorized, nil
	}
	var accepted []string
	rejected := 0
	for _, p := range providers {
		switch probeProvider(ctx, client, p) {
		case probeAccepted:
			accepted = append(accepted, p.Name)
		case probeRejected:
			rejected++
		}
	}
	if len(accepted) > 0 {
		evidence := authprobe.FileSource(path).WithMethod(domain.AgentAccountMethodAPIKey)
		evidence.Provider = strings.Join(accepted, ", ")
		ports.RecordAuthEvidence(ctx, evidence)
		return ports.AgentAuthStatusAuthorized, nil
	}
	if rejected == len(providers) {
		return ports.AgentAuthStatusUnauthorized, nil
	}
	return ports.AgentAuthStatusConfigured, nil
}

// probeProvider asks one provider whether it accepts its key. Anything other
// than a clear accept or an auth rejection (a timeout, a 5xx, an unknown
// provider type) is inconclusive.
func probeProvider(ctx context.Context, client *http.Client, p provider) probeVerdict {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var req *http.Request
	var err error
	accept := http.StatusOK
	switch p.Type {
	case "openai", "custom":
		endpoint := p.Endpoint
		if endpoint == "" && p.Type == "openai" {
			endpoint = "https://api.openai.com/v1"
		}
		if endpoint == "" {
			return probeInconclusive
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint, "/")+"/models", http.NoBody)
		if err == nil && p.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+p.APIKey)
		}
	case "anthropic":
		if p.APIKey == "" {
			return probeRejected
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, anthropicBaseURL(p.Endpoint)+"messages",
			bytes.NewReader([]byte(`{"model":"claude-haiku-4-5","max_tokens":1,"messages":[]}`)))
		if err == nil {
			req.Header.Set("x-api-key", p.APIKey)
			req.Header.Set("anthropic-version", "2023-06-01")
			req.Header.Set("content-type", "application/json")
		}
		// An empty conversation is invalid, and providers only say so to a
		// caller whose key they accepted.
		accept = http.StatusBadRequest
	case "gemini":
		if p.APIKey == "" {
			return probeRejected
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, "https://generativelanguage.googleapis.com/v1beta/models", http.NoBody)
		if err == nil {
			req.Header.Set("x-goog-api-key", p.APIKey)
		}
	default:
		return probeInconclusive
	}
	if err != nil {
		return probeInconclusive
	}
	resp, err := client.Do(req)
	if err != nil {
		return probeInconclusive
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	switch resp.StatusCode {
	case accept:
		return probeAccepted
	case http.StatusUnauthorized, http.StatusForbidden:
		return probeRejected
	}
	return probeInconclusive
}

// anthropicBaseURL normalizes an endpoint the way Stakpak does: a trailing
// "/messages" is dropped and the base ends in "/".
func anthropicBaseURL(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return "https://api.anthropic.com/v1/"
	}
	endpoint = strings.TrimSuffix(strings.TrimSuffix(endpoint, "/"), "/messages")
	return endpoint + "/"
}
