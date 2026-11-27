package pax

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// GetAgentHooks installs nothing. Pax refuses to add plugins or hooks from a
// shell, because the agent can run `pax` too: only `/plugins` typed in a Pax
// session can. A project's `.clai/settings.json` hooks table needs the same
// approval again for every folder, which every AO worktree is.
//
// A user who wants hook-reported turns adds AO's table once, in any Pax
// session (docs/harnesses/pax.md):
//
//	/plugins add ao pydantic_ai_pax.builtin_plugins.hooks '{"turn_start":[{"command":["ao","hooks","pax","turn-start"]}],"turn_end":[{"command":["ao","hooks","pax","turn-end"]}]}'
//
// Pax runs hook commands with its own environment, so AO_SESSION_ID reaches
// `ao hooks`, which does nothing outside an AO session.
func (*Plugin) GetAgentHooks(ctx context.Context, _ ports.WorkspaceHookConfig) error {
	return ctx.Err()
}

// DeriveActivityState maps the events of AO's Pax hook table. Pax passes
// {"event", "session_id", "cwd"} on stdin; only the event name matters.
func DeriveActivityState(event string, _ []byte) (domain.ActivityState, bool) {
	switch event {
	case "turn-start", "before-tool", "after-tool":
		return domain.ActivityActive, true
	case "turn-end":
		return domain.ActivityIdle, true
	default:
		return "", false
	}
}
