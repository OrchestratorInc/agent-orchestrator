package hermes

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var (
	_ ports.AgentAuthChecker            = (*Plugin)(nil)
	_ ports.AgentAuthDiagnosticsChecker = (*Plugin)(nil)
)

// hermesCredentialEnv are the inference-provider keys Hermes reads (its
// cli-config.yaml.example provider list). Tool and messaging tokens, such as
// TELEGRAM_BOT_TOKEN or a web-search key, are not model credentials.
var hermesCredentialEnv = []string{
	"OPENROUTER_API_KEY", "NOUS_API_KEY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY",
	"ZAI_API_KEY", "GLM_API_KEY", "KIMI_API_KEY", "MINIMAX_API_KEY", "MINIMAX_CN_API_KEY", "XAI_API_KEY",
	"DEEPSEEK_API_KEY", "HF_TOKEN", "NVIDIA_API_KEY", "XIAOMI_API_KEY", "ARCEEAI_API_KEY", "OLLAMA_API_KEY",
	"DEEPINFRA_API_KEY", "KILOCODE_API_KEY", "AI_GATEWAY_API_KEY", "GITHUB_TOKEN",
}

// AuthStatus implements the scoped native adapter contract.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	status, _, _, err := p.AuthStatusWithDiagnostics(ctx)
	return status, err
}

// AuthStatusWithDiagnostics asks Hermes which provider it resolves. Hermes has
// status inventory cannot verify credentials or prove one is missing. A resolved
// Z.AI route is verified against its selected endpoint; unsupported or inconclusive
// routes retain the credential-presence result.
func (p *Plugin) AuthStatusWithDiagnostics(ctx context.Context) (ports.AgentAuthStatus, domain.AgentAccountIdentity, []domain.AgentAuthDiagnostic, error) {
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return ports.AgentAuthStatusUnknown, domain.AgentAccountIdentity{}, nil, err
	}
	provider := ""
	status, steps, err := authprobe.CLIStatusWithDiagnostics(ctx, binary, [][]string{{"status"}}, 30*time.Second, func(output []byte) (ports.AgentAuthStatus, bool) {
		var status ports.AgentAuthStatus
		provider, status = providerFromStatus(string(output))
		return status, true
	})
	if err != nil {
		return ports.AgentAuthStatusUnknown, domain.AgentAccountIdentity{}, steps, err
	}
	if status != ports.AgentAuthStatusUnknown {
		identity := domain.CommandSource(binary, "status")
		identity.Provider = provider
		if status.HasCredential() {
			ports.RecordAuthEvidence(ctx, identity)
		}
		return verifyAuthentication(ctx, status, identity, steps)
	}
	identity, found := credentialStoreIdentity(ctx)
	if !found {
		return ports.AgentAuthStatusUnknown, domain.AgentAccountIdentity{}, steps, nil
	}
	ports.RecordAuthEvidence(ctx, identity)
	return verifyAuthentication(ctx, ports.AgentAuthStatusConfigured, identity, steps)
}

// providerFromStatus reads `hermes status`. Its "Provider:" row is a display
// label that falls back to the requested provider even without credentials,
// so only the "Providers:" row of connected credentials is evidence. That row
// is not a complete inventory: it omits keyless local routes (LM Studio,
// Actual Computer, custom endpoints) and SDK credential chains such as an AWS
// profile, so an empty or non-matching row stays unresolved and the
// credential stores then decide.
func providerFromStatus(output string) (string, ports.AgentAuthStatus) {
	selected, connected := "", []string(nil)
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "Provider":
			selected = value
		case "Providers":
			// "unknown" means Hermes's own inventory probe failed.
			if lower := strings.ToLower(value); value == "" || lower == "unknown" || strings.HasPrefix(lower, "none") || lower == "(none)" {
				continue
			}
			for _, name := range strings.Split(value, ",") {
				if name = strings.TrimSpace(name); name != "" {
					connected = append(connected, name)
				}
			}
		}
	}
	if len(connected) == 0 {
		return "", ports.AgentAuthStatusUnknown
	}
	switch lower := strings.ToLower(selected); {
	case lower == "" || lower == "auto" || lower == "(not set)":
		return strings.Join(connected, ", "), ports.AgentAuthStatusConfigured
	case slices.ContainsFunc(connected, func(name string) bool { return strings.EqualFold(name, selected) }):
		return selected, ports.AgentAuthStatusConfigured
	}
	return "", ports.AgentAuthStatusUnknown
}

// credentialStoreIdentity finds a credential in AO's environment, then Hermes's
// <HERMES_HOME>/.env (API keys) or auth.json (OAuth). Values are never read out.
func credentialStoreIdentity(ctx context.Context) (domain.AgentAccountIdentity, bool) {
	home, err := ResolveHome(nil)
	if err != nil {
		return domain.AgentAccountIdentity{}, false
	}
	// A key that a dotenv layer Hermes loads with override assigns, even to
	// empty, replaces the one AO's environment would pass down.
	if assigned, known := overridingAssignments(home); known {
		inherited := slices.DeleteFunc(slices.Clone(hermesCredentialEnv), func(name string) bool {
			_, ok := assigned[processEnvName(name)]
			return ok
		})
		if name := authprobe.CredentialEnvironment(ctx, domain.AgentAccountMethodAPIKey, inherited...); name != "" {
			return domain.EnvironmentSource(name).WithMethod(domain.AgentAccountMethodAPIKey), true
		}
	}
	if path := filepath.Join(home, ".env"); envFileHasCredential(path) {
		return authprobe.FileSource(path).WithMethod(domain.AgentAccountMethodAPIKey), true
	}
	if path := filepath.Join(home, "auth.json"); authFileHasCredential(path) {
		return authprobe.FileSource(path).WithMethod(domain.AgentAccountMethodAccount), true
	}
	return domain.AgentAccountIdentity{}, false
}

// overridingAssignments collects the names set by the dotenv layers Hermes
// loads with override (env_loader.load_hermes_dotenv): <HERMES_HOME>/.env
// when it exists, otherwise the source checkout's project .env, and then any
// managed scope's .env. known is false when a layer cannot be read the way
// Hermes reads it, so no inherited key can be trusted.
func overridingAssignments(home string) (assigned map[string]string, known bool) {
	assigned = map[string]string{}
	layers := []string{filepath.Join(home, ".env")}
	if _, err := os.Stat(layers[0]); errors.Is(err, os.ErrNotExist) {
		roots := sourceRoots(home, nil)
		layers = make([]string, 0, len(roots)+2)
		for _, root := range roots {
			layers = append(layers, filepath.Join(root, ".env"))
		}
	}
	for _, managed := range []string{os.Getenv("HERMES_MANAGED_DIR"), "/etc/hermes"} {
		if managed = pyStrip(managed); managed != "" {
			layers = append(layers, filepath.Join(managed, ".env"))
		}
	}
	// Each layer resolves ${VAR} against the process environment as earlier
	// layers left it.
	lookup := func(name string) (string, bool) {
		if value, ok := assigned[processEnvName(name)]; ok {
			return value, true
		}
		return os.LookupEnv(name)
	}
	for _, layer := range layers {
		bindings, err := dotenvBindings(layer)
		if err != nil {
			return nil, false
		}
		maps.Copy(assigned, dotenvEnvironment(bindings, lookup))
	}
	return assigned, true
}

// envFileHasCredential reads the value Hermes ends up with: a later empty
// assignment clears an earlier key, and a blank value is no key.
func envFileHasCredential(path string) bool {
	bindings, _ := dotenvBindings(path)
	env := dotenvEnvironment(bindings, os.LookupEnv)
	for _, name := range hermesCredentialEnv {
		if strings.TrimFunc(env[processEnvName(name)], pyIsSpace) != "" {
			return true
		}
	}
	return false
}

// authFileHasCredential looks for a secret held in Hermes's auth store: a
// token or key in a provider state or credential-pool entry. Hermes keeps a
// provider's empty pool list after its last credential is removed, and keeps
// secret-free references to borrowed environment keys, which it never uses
// once that key is gone. Older stores held provider states at the top level.
func authFileHasCredential(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var store map[string]any
	if json.Unmarshal(b, &store) != nil {
		return false
	}
	providers, hasProviders := store["providers"].(map[string]any)
	pool, hasPool := store["credential_pool"].(map[string]any)
	if !hasProviders && !hasPool {
		providers = store
	}
	for _, state := range providers {
		if holdsSecret(state) {
			return true
		}
	}
	for _, entries := range pool {
		entries, _ := entries.([]any)
		if slices.ContainsFunc(entries, holdsSecret) {
			return true
		}
	}
	return false
}

// holdsSecret reports whether a stored record carries a non-blank token or
// key field, such as access_token, refresh_token or api_key.
func holdsSecret(record any) bool {
	fields, _ := record.(map[string]any)
	for name, value := range fields {
		value, ok := value.(string)
		if ok && (strings.HasSuffix(name, "token") || strings.HasSuffix(name, "_key")) && strings.TrimFunc(value, pyIsSpace) != "" {
			return true
		}
	}
	return false
}
