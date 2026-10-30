package telemetrymeta

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

// SessionKind is the exported kind of a session. Standalone sessions have no
// project, even when their stored session kind is worker.
func SessionKind(rec domain.SessionRecord) string {
	if rec.IsStandalone() {
		return "standalone"
	}
	return string(rec.Kind)
}
