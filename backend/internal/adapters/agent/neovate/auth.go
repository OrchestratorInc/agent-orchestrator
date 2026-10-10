package neovate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var credentialEnv = []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GOOGLE_API_KEY", "GOOGLE_GENERATIVE_AI_API_KEY", "DEEPSEEK_API_KEY", "OPENROUTER_API_KEY"}

// AuthStatus reports local configuration, never proof of provider authorization.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	for _, name := range credentialEnv {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return ports.AgentAuthStatusConfigured, nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	data, err := os.ReadFile(filepath.Join(home, ".neovate", "config.json")) //nolint:gosec // official local provider config
	if errors.Is(err, os.ErrNotExist) {
		return ports.AgentAuthStatusUnknown, nil
	}
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	var config struct {
		Provider map[string]struct {
			Options struct {
				APIKey string `json:"apiKey"`
			} `json:"options"`
		} `json:"provider"`
	}
	if json.Unmarshal(data, &config) != nil {
		return ports.AgentAuthStatusUnknown, nil //nolint:nilerr // malformed local config does not establish provider authorization
	}
	for _, provider := range config.Provider {
		if strings.TrimSpace(provider.Options.APIKey) != "" {
			return ports.AgentAuthStatusConfigured, nil
		}
	}
	return ports.AgentAuthStatusUnknown, nil
}
