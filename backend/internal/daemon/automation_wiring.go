package daemon

import (
	"context"
	"errors"
	"log/slog"
	"time"

	automationobserver "github.com/aoagents/agent-orchestrator/backend/internal/observe/automations"
	automationsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/automation"
	remindersvc "github.com/aoagents/agent-orchestrator/backend/internal/service/reminder"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// startAutomations repairs crash-interrupted durable state before launching the
// cadence-only observer. Reconciliation is best effort so one malformed legacy
// row cannot prevent daemon readiness. Session reminders ride the same tick.
func startAutomations(ctx context.Context, store *sqlite.Store, sessions *sessionsvc.Service, reminders *remindersvc.Service, logger *slog.Logger) (*automationsvc.Service, <-chan struct{}) {
	service := automationsvc.New(automationsvc.Deps{Store: store, Spawner: sessions})
	if err := service.Reconcile(ctx); err != nil {
		logger.Warn("automation startup reconciliation completed with errors", "err", err)
	}
	observer := automationobserver.New(schedulerTick{service, reminders}, automationobserver.Config{Logger: logger})
	return service, observer.Start(ctx)
}

// schedulerTick advances every durable schedule on one cadence. A failing
// poller does not stop the others from running.
type schedulerTick []automationobserver.Poller

func (t schedulerTick) Tick(ctx context.Context, at time.Time) error {
	var problems []error
	for _, poller := range t {
		if poller == nil {
			continue
		}
		if err := poller.Tick(ctx, at); err != nil {
			problems = append(problems, err)
		}
	}
	return errors.Join(problems...)
}
