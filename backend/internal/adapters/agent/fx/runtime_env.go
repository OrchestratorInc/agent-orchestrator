package fx

import (
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/fx/herdr"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.AgentRuntimeLaunchEnv = (*Plugin)(nil)

func (p *Plugin) AugmentRuntimeLaunchEnv(env map[string]string, dataDir string, sessionID domain.SessionID, launchID string) {
	env["HERDR_SOCKET_PATH"] = herdr.SocketPath(dataDir)
	env["HERDR_PANE_ID"] = herdr.PaneID(sessionID, launchID)
}
