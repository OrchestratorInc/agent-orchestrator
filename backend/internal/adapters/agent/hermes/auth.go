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

var hermesCredentialEnv = []string{
	"OPENROUTER_API_KEY", "NOUS_API_KEY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY",
	"ZAI_API_KEY", "GLM_API_KEY", "KIMI_API_KEY", "MINIMAX_API_KEY", "MINIMAX_CN_API_KEY", "XAI_API_KEY",
	"DEEPSEEK_API_KEY", "HF_TOKEN", "NVIDIA_API_KEY", "XIAOMI_API_KEY", "ARCEEAI_API_KEY", "OLLAMA_API_KEY",
	"DEEPINFRA_API_KEY", "KILOCODE_API_KEY", "AI_GATEWAY_API_KEY", "GITHUB_TOKEN",
}

func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	status, _, _, err := p.AuthStatusWithDiagnostics(ctx)
	return status, err
}

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

func credentialStoreIdentity(ctx context.Context) (domain.AgentAccountIdentity, bool) {
	home, err := ResolveHome(nil)
	if err != nil {
		return domain.AgentAccountIdentity{}, false
	}
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
