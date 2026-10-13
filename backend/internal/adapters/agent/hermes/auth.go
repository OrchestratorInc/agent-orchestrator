package hermes

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.AgentAuthChecker = (*Plugin)(nil)

// AuthStatus returns the plugin's local authentication status. Hermes has no
// non-interactive `auth status` command: unknown positional arguments launch
// its interactive agent and must never be used as probes.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	if _, err := p.hermesBinary(ctx); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	if status, ok, err := hermesLocalAuthStatus(nil); err != nil {
		return ports.AgentAuthStatusUnknown, err
	} else if ok {
		return status, nil
	}
	return ports.AgentAuthStatusUnknown, nil
}

// hermesProviderKeyEnvVars are the provider API keys Hermes reads from the
// process environment. Any one of them is credential evidence.
var hermesProviderKeyEnvVars = []string{
	"OPENAI_API_KEY",
	"ANTHROPIC_API_KEY",
	"GEMINI_API_KEY",
	"GOOGLE_API_KEY",
	"OPENROUTER_API_KEY",
	"DEEPSEEK_API_KEY",
	"XAI_API_KEY",
	"MOONSHOT_API_KEY",
	"MINIMAX_API_KEY",
	"ZAI_API_KEY",
	"ZHIPU_API_KEY",
}

func hermesLocalAuthStatus(env map[string]string) (ports.AgentAuthStatus, bool, error) {
	for _, name := range hermesProviderKeyEnvVars {
		if envValue(env, name) != "" {
			return ports.AgentAuthStatusAuthorized, true, nil
		}
	}

	home, ok := hermesHome(env)
	if !ok {
		return ports.AgentAuthStatusUnknown, false, nil
	}
	return hermesAuthJSONStatus(filepath.Join(home, "auth.json"))
}

// hermesAuthJSON inspects the shape of ~/.hermes/auth.json without ever
// loading credential values into memory beyond existence checks. The file may
// hold `providers` OAuth/token entries, a `credential_pool` of pooled
// credentials, or provider `detected_endpoint` runtime credentials — any of
// which means Hermes has something to authenticate with.
type hermesAuthJSON struct {
	Providers      map[string]json.RawMessage `json:"providers"`
	CredentialPool map[string]json.RawMessage `json:"credential_pool"`
}

func hermesAuthJSONStatus(path string) (ports.AgentAuthStatus, bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ports.AgentAuthStatusUnknown, false, nil
	}
	if err != nil {
		return ports.AgentAuthStatusUnknown, false, err
	}
	if strings.TrimSpace(string(data)) == "" {
		return ports.AgentAuthStatusUnknown, false, nil
	}

	var auth hermesAuthJSON
	if err := json.Unmarshal(data, &auth); err != nil {
		return ports.AgentAuthStatusUnknown, false, err
	}
	if len(auth.Providers) > 0 || len(auth.CredentialPool) > 0 {
		return ports.AgentAuthStatusAuthorized, true, nil
	}
	return ports.AgentAuthStatusUnknown, false, nil
}

// envValue reads from the overlay map when one is supplied, falling back to the
// process environment, so tests can inject values without mutating global state.
func envValue(env map[string]string, name string) string {
	if env != nil {
		return strings.TrimSpace(env[name])
	}
	return strings.TrimSpace(os.Getenv(name))
}
