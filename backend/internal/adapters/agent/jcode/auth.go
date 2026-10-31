package jcode

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const (
	authStatusTimeout = 3 * time.Second
	usageTimeout      = 6 * time.Second
)

// AuthStatus reports JCode sign-in without trusting presence.
//
// `jcode auth status --json` only checks that credentials exist (and their
// stored validation can be a week old), so a provider it reports available is
// at most configured. Authorized needs a live round trip: `jcode usage --json`
// asks each connected provider's usage API with the stored credential and
// spends no model tokens. A provider report without an error proves the
// credential was accepted. `jcode auth-test` is not used: it rotates OAuth
// refresh tokens, and its smoke steps send prompts.
//
// A keyless local provider (Ollama, LM Studio) has no credential and no usage
// API. When jcode reports the active provider as a plain local endpoint, AO
// asks that loopback endpoint for one token with jcode's selected model; an
// answered completion proves the provider jcode will use can run the model.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	statusArgs := []string{"--no-update", "auth", "status", "--json"}
	out, started, err := runProbe(ctx, binary, statusArgs, authStatusTimeout)
	var status struct {
		Providers []jcodeProviderStatus `json:"providers"`
	}
	if err == nil {
		err = json.Unmarshal(out, &status)
	}
	if err != nil {
		ports.RecordAuthDiagnostic(ctx, authprobe.CommandStep(binary, statusArgs, ports.AgentAuthStatusUnknown, err, ctx.Err(), started))
		return ports.AgentAuthStatusUnknown, err
	}
	present := false
	for _, provider := range status.Providers {
		// expired OAuth can still be renewed by JCode on its next request.
		if provider.Status == "available" || provider.Status == "expired" {
			present = true
		}
	}
	if !present {
		ports.RecordAuthDiagnostic(ctx, authprobe.CommandStep(binary, statusArgs, ports.AgentAuthStatusUnauthorized, nil, nil, started))
		return ports.AgentAuthStatusUnauthorized, nil
	}
	ports.RecordAuthDiagnostic(ctx, authprobe.CommandStep(binary, statusArgs, ports.AgentAuthStatusConfigured, nil, nil, started))

	usageArgs := []string{"--no-update", "usage", "--json"}
	method := strings.Join(append([]string{filepath.Base(binary)}, usageArgs...), " ")
	provider, model, usagePrefix, err := p.activeUsageProvider(ctx, binary)
	if errors.Is(err, errNoUsageCheck) {
		if base, ok := localEndpoint(status.Providers, provider); ok {
			verdict, step := authprobe.VerifyConnection(ctx, authprobe.Connection{Provider: provider, BaseURL: base, Model: model, Keyless: true}, nil)
			step.Method = "local endpoint completion"
			ports.RecordAuthDiagnostic(ctx, step)
			return verdict, nil
		}
		ports.RecordAuthDiagnostic(ctx, providerDiagnostic(method, "provider_unsupported", provider, time.Now()))
		return ports.AgentAuthStatusConfigured, nil
	}
	started = time.Now()
	if err == nil {
		out, started, err = runProbe(ctx, binary, usageArgs, usageTimeout)
	}
	if err != nil {
		ports.RecordAuthDiagnostic(ctx, authprobe.CommandStep(binary, usageArgs, ports.AgentAuthStatusUnknown, err, ctx.Err(), started))
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ports.AgentAuthStatusUnknown, ctxErr
		}
		return ports.AgentAuthStatusConfigured, nil
	}
	if !usageVerifies(out, usagePrefix) {
		// A quota, rate-limit or rejected usage request does not prove the
		// login is unusable for inference, so it stays configured.
		ports.RecordAuthDiagnostic(ctx, providerDiagnostic(method, "provider_unverified", provider, started))
		return ports.AgentAuthStatusConfigured, nil
	}
	ports.RecordAuthDiagnostic(ctx, providerDiagnostic(method, "provider_accepted", provider, started))
	return ports.AgentAuthStatusAuthorized, nil
}

func providerDiagnostic(method, code, provider string, started time.Time) domain.AgentAuthDiagnostic {
	step := domain.NewAgentAuthDiagnostic(method, code)
	step.Provider = provider
	step.DurationMs = time.Since(started).Milliseconds()
	return step
}

// usageProviders maps the provider JCode resolves for new sessions (`provider
// current`) onto the report names `jcode usage` gives that provider's account.
// A provider without a usage endpoint (most API-key providers) has no live
// check and stays configured.
var usageProviders = map[string]string{
	"claude":      "Anthropic",
	"anthropic":   "Anthropic",
	"openai":      "OpenAI",
	"openrouter":  "OpenRouter",
	"copilot":     "GitHub Copilot",
	"antigravity": "Antigravity",
	"cursor":      "Cursor",
	"jcode":       "Jcode subscription",
}

var errNoUsageCheck = errors.New("the active jcode provider has no live credential check")

// activeUsageProvider returns the provider and model JCode resolves for new
// sessions and the usage report name that belongs to the provider.
func (p *Plugin) activeUsageProvider(ctx context.Context, binary string) (provider, model, prefix string, err error) {
	out, _, err := runProbe(ctx, binary, []string{"--no-update", "provider", "current", "--json"}, authStatusTimeout)
	if err != nil {
		return "", "", "", err
	}
	var current struct {
		Resolved string `json:"resolved_provider"`
		Model    string `json:"selected_model"`
	}
	if err := json.Unmarshal(out, &current); err != nil {
		return "", "", "", err
	}
	provider, model = strings.TrimSpace(current.Resolved), strings.TrimSpace(current.Model)
	prefix, ok := usageProviders[strings.ToLower(provider)]
	if !ok {
		return provider, model, "", errNoUsageCheck
	}
	return provider, model, prefix, nil
}

type jcodeProviderStatus struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Method string `json:"method"`
}

// localEndpointMethod is how `jcode auth status` describes a keyless local
// provider set up with `jcode login`, e.g. "local endpoint (`http://localhost:11434/v1`)".
// An entry that also stores an optional API key says "+ optional API key" and
// does not match: that key is not AO's to send.
var localEndpointMethod = regexp.MustCompile("^local endpoint \\(`(https?://[^`]+)`\\)$")

// localEndpoint returns the base URL jcode reports for the active provider
// when that provider is an available keyless local endpoint.
func localEndpoint(providers []jcodeProviderStatus, active string) (string, bool) {
	normalize := func(value string) string {
		return strings.NewReplacer(" ", "", "-", "", "_", "").Replace(strings.ToLower(strings.TrimSpace(value)))
	}
	for _, provider := range providers {
		if provider.Status != "available" || normalize(provider.ID) != normalize(active) {
			continue
		}
		if match := localEndpointMethod.FindStringSubmatch(strings.TrimSpace(provider.Method)); match != nil {
			return match[1], true
		}
	}
	return "", false
}

// usageVerifies reports whether the active provider's usage report came back
// without an error, which needs the provider to accept the credential.
func usageVerifies(out []byte, prefix string) bool {
	var usage struct {
		Providers []struct {
			Name  string  `json:"provider_name"`
			Error *string `json:"error"`
		} `json:"providers"`
	}
	if json.Unmarshal(out, &usage) != nil {
		return false
	}
	for _, provider := range usage.Providers {
		if provider.Name != prefix && !strings.HasPrefix(provider.Name, prefix+" ") {
			continue
		}
		if provider.Error == nil || strings.TrimSpace(*provider.Error) == "" {
			return true
		}
	}
	return false
}

func runProbe(ctx context.Context, binary string, args []string, timeout time.Duration) ([]byte, time.Time, error) {
	started := time.Now()
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := process.CommandContext(probeCtx, binary, args...) //nolint:gosec // adapter-resolved binary, static args
	cmd.Env = append(os.Environ(), "JCODE_NON_INTERACTIVE=1", "JCODE_CHECK_UPDATES=0", "JCODE_NO_AUTO_UPDATE=1")
	out, err := cmd.Output()
	return out, started, err
}
