package telemetrymeta

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

func SessionKind(kind domain.SessionKind, projectID domain.ProjectID) string {
	if projectID == "" {
		return "standalone"
	}
	return string(kind)
}
