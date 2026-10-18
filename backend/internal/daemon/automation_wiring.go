package daemon

import (
	"context"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	automationobserver "github.com/aoagents/agent-orchestrator/backend/internal/observe/automations"
	automationsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/automation"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// startAutomations repairs crash-interrupted durable state before launching the
// cadence observer. Reconciliation is best effort so one malformed legacy row
// cannot prevent daemon readiness. An archived or merged session queues its
// session-ended runs and wakes the observer so they start right away.
func startAutomations(ctx context.Context, store *sqlite.Store, sessions *sessionsvc.Service, logger *slog.Logger) (*automationsvc.Service, <-chan struct{}) {
	service := automationsvc.New(automationsvc.Deps{Store: store, Spawner: sessions})
	if err := service.Reconcile(ctx); err != nil {
		logger.Warn("automation startup reconciliation completed with errors", "err", err)
	}
	observer := automationobserver.New(service, automationobserver.Config{Logger: logger})
	sessions.SetEndedHook(func(ctx context.Context, id domain.SessionID) {
		queued, err := service.SessionEnded(ctx, id)
		if err != nil {
			logger.Warn("session-ended automations failed", "session", id, "err", err)
		}
		if queued {
			observer.Wake()
		}
	})
	return service, observer.Start(ctx)
}
