package chat

import (
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func applyClaudeAccountsManagerEnv(source map[string]string, route *ports.AccountsManagerLaunchRoute) map[string]string {
	env := make(map[string]string, len(source)+2)
	for key, value := range source {
		env[key] = value
	}
	for key := range env {
		if strings.EqualFold(key, "ANTHROPIC_API_KEY") || strings.EqualFold(key, "ANTHROPIC_AUTH_TOKEN") || strings.EqualFold(key, "ANTHROPIC_BASE_URL") || strings.EqualFold(key, "CLAUDE_CODE_OAUTH_TOKEN") {
			delete(env, key)
		}
	}
	env["ANTHROPIC_BASE_URL"] = route.BaseURL
	env["ANTHROPIC_AUTH_TOKEN"] = route.Token
	return env
}
