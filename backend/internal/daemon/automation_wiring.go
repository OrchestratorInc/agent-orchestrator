package daemon

import (
	"context"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	automationobserver "github.com/aoagents/agent-orchestrator/backend/internal/observe/automations"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	automationsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/automation"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// startAutomations repairs crash-interrupted durable state before launching the
// cadence-only observer. Reconciliation is best effort so one malformed legacy
// row cannot prevent daemon readiness.
func startAutomations(ctx context.Context, store *sqlite.Store, sessions *sessionsvc.Service, logger *slog.Logger) (*automationsvc.Service, <-chan struct{}) {
	service := automationsvc.New(automationsvc.Deps{Store: store, Spawner: automationSpawner{sessions: sessions}})
	if err := service.Reconcile(ctx); err != nil {
		logger.Warn("automation startup reconciliation completed with errors", "err", err)
	}
	observer := automationobserver.New(service, automationobserver.Config{Logger: logger})
	return service, observer.Start(ctx)
}

// automationSpawner keeps the automation boundary's teardown result limited
// to whether the session worktree was freed; interactive archiving details
// belong to the session API.
type automationSpawner struct {
	sessions *sessionsvc.Service
}

func (s automationSpawner) Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.Session, int, int, error) {
	return s.sessions.Spawn(ctx, cfg)
}

func (s automationSpawner) Kill(ctx context.Context, id domain.SessionID) (bool, error) {
	outcome, err := s.sessions.Kill(ctx, id)
	return outcome.Freed, err
}
