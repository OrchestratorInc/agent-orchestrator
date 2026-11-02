package sessionmanager

import (
	"context"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Goose has no per-session lock or live-session registry. A writer briefly
// opens the shared database, so lsof on it cannot identify a conversation.
// The native CLI's explicit --session-id remains the resume authority.
func gooseImportOwnedElsewhere(context.Context, domain.SessionRecord) bool { return false }

func (m *Manager) applyImportedGooseConfigEnv(ctx context.Context, rec domain.SessionRecord, env map[string]string) {
	source := rec.Metadata.ImportSource
	if agent, ok := m.agents.Agent(rec.Harness); ok {
		if provider, ok := agent.(ports.AgentNativeSessionConfigProvider); ok {
			if current, err := provider.NativeSessionConfigDir(ctx, env); err == nil && sameDirectory(current, source.ConfigDir) {
				return
			}
		}
	}
	if filepath.Base(source.ConfigDir) == "data" {
		setProtectedEnv(env, "GOOSE_PATH_ROOT", filepath.Dir(source.ConfigDir), envKeysCaseInsensitive)
		return
	}
	// The XDG data root is goose/'s parent; it leaves config/auth unchanged.
	setProtectedEnv(env, "GOOSE_PATH_ROOT", "", envKeysCaseInsensitive)
	setProtectedEnv(env, "XDG_DATA_HOME", filepath.Dir(source.ConfigDir), envKeysCaseInsensitive)
}
