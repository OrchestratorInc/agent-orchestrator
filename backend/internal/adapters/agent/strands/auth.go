package strands

import (
	"context"
	"os"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// AuthStatus reports only configuration, never provider authorization. Strands
// has no safe noninteractive CLI auth-status command. Model modules may use
// arbitrary credential sources, so their authentication remains unknown.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	if _, err := p.ResolveBinary(ctx); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	cfg, err := readNativeConfig(ctx)
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	if cfg.AgentProject != "" || len(cfg.Profile.ModelModule) > 0 && string(cfg.Profile.ModelModule) != "null" {
		return ports.AgentAuthStatusUnknown, nil
	}
	provider, _, hasPrefix := strings.Cut(cfg.Profile.Model, "/")
	if !hasPrefix {
		provider = "bedrock"
	}
	key := map[string]string{"anthropic": "ANTHROPIC_API_KEY", "openai": "OPENAI_API_KEY", "google": "GEMINI_API_KEY", "litellm": "LITELLM_API_KEY", "bedrock": "AWS_BEARER_TOKEN_BEDROCK", "bedrock-mantle": "AWS_BEARER_TOKEN_BEDROCK"}[provider]
	if key != "" && strings.TrimSpace(os.Getenv(key)) != "" {
		return ports.AgentAuthStatusConfigured, nil
	}
	if strings.HasPrefix(provider, "bedrock") && os.Getenv("AWS_ACCESS_KEY_ID") != "" && os.Getenv("AWS_SECRET_ACCESS_KEY") != "" {
		return ports.AgentAuthStatusConfigured, nil
	}
	return ports.AgentAuthStatusUnknown, nil
}
