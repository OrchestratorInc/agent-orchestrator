package claudecode

import (
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func (p *Plugin) DetectTerminalActivity(output string) (domain.ActivityState, bool) {
	if strings.Contains(strings.ToLower(output), "login expired") &&
		strings.Contains(strings.ToLower(output), "run /login") {
		return domain.ActivityWaitingInput, true
	}
	if p.InspectTerminalSurface(output).Work == ports.TerminalSurfaceWorkIdle {
		return domain.ActivityIdle, true
	}
	return "", false
}
