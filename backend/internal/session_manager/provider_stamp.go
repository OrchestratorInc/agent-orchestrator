package sessionmanager

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/pkg/agentcreds"
)

// stampSessionProvider resolves the gateway identity (base URL and model) the
// agent process launched at workingDir with env would see, using the same
// resolver the launch-auth path runs. The result is persisted as the session's
// provider stamp so a later gateway switch can detect which running sessions
// would relaunch on a different provider (issue #6096).
//
// Token-only changes are deliberately not stamped — the stamp compares base
// URL and model only.
//
// ponytail: keep in sync with service/session staleness resolution
// (provider_apply.go); both sides must resolve the same way.
func stampSessionProvider(ctx context.Context, workingDir string, env map[string]string) (string, string) {
	resolved := agentcreds.ResolveClaudeSettings(ctx, workingDir, env, agentcreds.ResolveOptions{})
	return resolved.Env["ANTHROPIC_BASE_URL"], resolved.Env["ANTHROPIC_MODEL"]
}
