package sessionmanager

import (
	"context"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TranscriptPath returns the native transcript of a session's current agent,
// resolved the same way agent switching preserves it, or "" when the harness
// cannot locate one. The transcript lives in the agent's own config home, so
// it outlives the session's worktree.
func (m *Manager) TranscriptPath(ctx context.Context, id domain.SessionID) string {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok {
		return ""
	}
	agent, ok := m.agents.Agent(rec.Harness)
	if !ok {
		return ""
	}
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return ""
	}
	env := m.runtimeEnv(rec.ID, rec.ProjectID, rec.IssueID, project.Config.Env)
	m.augmentAgentRuntimeEnv(agent, env)
	configDir, err := nativeConfigDir(ctx, agent, env)
	if err != nil {
		return ""
	}
	path := safeNativeTranscriptPath(ctx, rec.Metadata.NativeTranscriptPath, configDir)
	nativeID := strings.TrimSpace(rec.Metadata.AgentSessionID)
	if domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat {
		nativeID = strings.TrimSpace(rec.Metadata.ProviderConversationID)
	}
	if locator, ok := agent.(ports.AgentTranscriptLocator); ok && nativeID != "" {
		ref := ports.NativeSessionRef{NativeSessionID: nativeID, ConfigDir: configDir}
		if located, found, err := locator.LocateTranscript(ctx, ref); err == nil && found {
			path = safeNativeTranscriptPath(ctx, located, configDir)
		}
	}
	return path
}
