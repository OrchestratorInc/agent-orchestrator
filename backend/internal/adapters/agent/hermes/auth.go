package hermes

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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

// hermesCredentialEnv are the provider keys Hermes reads from its environment.
var hermesCredentialEnv = []string{"OPENROUTER_API_KEY", "NOUS_API_KEY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "ZAI_API_KEY", "GLM_API_KEY", "KIMI_API_KEY", "MINIMAX_API_KEY", "XAI_API_KEY", "DEEPSEEK_API_KEY", "HF_TOKEN"}

// AuthStatus implements the scoped native adapter contract.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	status, _, _, err := p.AuthStatusWithDiagnostics(ctx)
	return status, err
}

// AuthStatusWithDiagnostics asks Hermes which provider it resolves. Hermes has
// no status command that verifies a credential, so a resolved provider is
// configured, not authorized; no provider at all means Hermes cannot run.
// When the probe is inconclusive, Hermes's own credential stores decide.
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
		return status, identity, steps, nil
	}
	identity, found := credentialStoreIdentity(ctx)
	if !found {
		return ports.AgentAuthStatusUnknown, domain.AgentAccountIdentity{}, steps, nil
	}
	ports.RecordAuthEvidence(ctx, identity)
	return ports.AgentAuthStatusConfigured, identity, steps, nil
}

// providerFromStatus reads the active provider from `hermes status`, whose
// summary prints a "Provider:" row. A missing row leaves the verdict unknown.
func providerFromStatus(output string) (string, ports.AgentAuthStatus) {
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || strings.TrimSpace(key) != "Provider" {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(value) {
		case "", "(not set)", "none", "(none)", "not configured":
			return "", ports.AgentAuthStatusUnauthorized
		}
		return value, ports.AgentAuthStatusConfigured
	}
	return "", ports.AgentAuthStatusUnknown
}

// credentialStoreIdentity finds a credential in AO's environment, then Hermes's
// <HERMES_HOME>/.env (API keys) or auth.json (OAuth). Values are never read out.
func credentialStoreIdentity(ctx context.Context) (domain.AgentAccountIdentity, bool) {
	if name := authprobe.CredentialEnvironment(ctx, domain.AgentAccountMethodAPIKey, hermesCredentialEnv...); name != "" {
		return domain.EnvironmentSource(name).WithMethod(domain.AgentAccountMethodAPIKey), true
	}
	home, err := resolveHome(nil)
	if err != nil {
		return domain.AgentAccountIdentity{}, false
	}
	if path := filepath.Join(home, ".env"); envFileHasCredential(path) {
		return authprobe.FileSource(path).WithMethod(domain.AgentAccountMethodAPIKey), true
	}
	if path := filepath.Join(home, "auth.json"); authFileHasCredential(path) {
		return authprobe.FileSource(path).WithMethod(domain.AgentAccountMethodAccount), true
	}
	return domain.AgentAccountIdentity{}, false
}

func envFileHasCredential(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	scanner := bufio.NewScanner(bytes.NewReader(b))
	for scanner.Scan() {
		key, value, ok := strings.Cut(strings.TrimPrefix(strings.TrimSpace(scanner.Text()), "export "), "=")
		key = strings.TrimSpace(key)
		if !ok || strings.HasPrefix(key, "#") || strings.Trim(strings.TrimSpace(value), `"'`) == "" {
			continue
		}
		if strings.HasSuffix(key, "_API_KEY") || strings.HasSuffix(key, "_TOKEN") {
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
	for _, value := range store {
		switch v := value.(type) {
		case map[string]any:
			if len(v) > 0 {
				return true
			}
		case []any:
			if len(v) > 0 {
				return true
			}
		}
	}
	return false
}
