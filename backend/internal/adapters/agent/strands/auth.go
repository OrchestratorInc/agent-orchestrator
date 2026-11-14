package strands

import (
	"context"
	"os"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// AuthStatus verifies supported native providers using the setup wizard's
// authenticated catalog discovery. Unsupported or inconclusive routes retain
// configuration evidence, and custom model modules remain unknown.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	cfg, _, err := readNativeConfig(ctx)
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	if cfg.AgentProject != "" || len(cfg.Profile.ModelModule) > 0 && string(cfg.Profile.ModelModule) != "null" {
		return ports.AgentAuthStatusUnknown, nil
	}
	if status, err := probeNativeAuthentication(ctx, binary); err == nil {
		return status, nil
	}
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, err
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
