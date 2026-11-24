package httpd

import (
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/config"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/terminal"
)

// newTestRouter builds a router with empty API and control deps. It is the
// test-only convenience that used to be the exported NewRouter wrapper; keeping
// it here leaves the package's exported surface to the production constructors.
func newTestRouter(cfg config.Config, log *slog.Logger, termMgr *terminal.Manager) chi.Router {
	return NewRouterWithControl(cfg, log, termMgr, APIDeps{}, ControlDeps{})
}
