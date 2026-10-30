package telemetrymeta

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

// SessionKind distinguishes standalone sessions from project workers.
func SessionKind(kind domain.SessionKind, projectID domain.ProjectID) string {
	if projectID == "" {
		return "standalone"
	}
	return string(kind)
}
