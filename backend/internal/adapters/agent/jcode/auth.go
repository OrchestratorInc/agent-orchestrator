package jcode

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
// jcode's keyless local Ollama mode has no account and no usage API. When it
// is the active provider, AO reads the inventory of the loopback Ollama server
// jcode reports and is authorized only when that server answers with jcode's
// selected model installed. No credential is involved, so the diagnostic says
// the local provider is available, never that a credential was accepted.
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
		if inventory, ok := ollamaInventoryURL(status.Providers); ok && strings.EqualFold(provider, "ollama") {
			return localOllamaAvailability(ctx, inventory, provider, model), nil
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

// localOllamaAvailability checks the local Ollama server behind jcode's
// keyless Ollama mode for the model jcode will use.
func localOllamaAvailability(ctx context.Context, inventory, provider, model string) ports.AgentAuthStatus {
	started := time.Now()
	method := "GET " + inventory
	installed, err := InstalledOllamaModels(ctx, inventory)
	switch {
	case err != nil:
		code := "provider_unreachable"
		if ctx.Err() != nil {
			code = "probe_cancelled"
		} else if errors.Is(err, context.DeadlineExceeded) {
			code = "probe_timeout"
		}
		ports.RecordAuthDiagnostic(ctx, providerDiagnostic(method, code, provider, started))
		return ports.AgentAuthStatusConfigured
	case !OllamaModelInstalled(installed, model):
		ports.RecordAuthDiagnostic(ctx, providerDiagnostic(method, "model_selection_unresolved", provider, started))
		return ports.AgentAuthStatusConfigured
	}
	ports.RecordAuthDiagnostic(ctx, providerDiagnostic(method, "local_provider_available", provider, started))
	return ports.AgentAuthStatusAuthorized
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
