package letta

import (
	"context"
	"os"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// AuthStatus never upgrades credential presence to verified authorization.
// Letta stores Cloud OAuth credentials in the OS keychain and supports local
// provider stores. Without a documented read-only status command, those remain
// unknown; native setup/model discovery owns their validation.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	if _, err := p.ResolveBinary(ctx); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	for _, key := range []string{"LETTA_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY", "GOOGLE_GENERATIVE_AI_API_KEY"} {
		if strings.TrimSpace(os.Getenv(key)) != "" {
			return ports.AgentAuthStatusConfigured, nil
		}
	}
	return ports.AgentAuthStatusUnknown, nil
}
