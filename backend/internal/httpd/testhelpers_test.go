package httpd

import (
	"log/slog"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/terminal"
)

func newTestRouter(cfg config.Config, log *slog.Logger, termMgr *terminal.Manager) chi.Router {
	return NewRouterWithControl(cfg, log, termMgr, APIDeps{}, ControlDeps{})
}
