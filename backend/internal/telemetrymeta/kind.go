package telemetrymeta

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

// SessionKind is the exported kind of a session. Standalone sessions have no
// project, even when their stored session kind is worker.
func SessionKind(kind domain.SessionKind, projectID domain.ProjectID) string {
	if projectID == "" {
		return "standalone"
	}
	return string(kind)
}
