package daemon

import (
	"context"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/sentryobs"
	awakesvc "github.com/aoagents/agent-orchestrator/backend/internal/service/awake"
)

// startAwake isolates optional wake intent failures from daemon startup. Preserve
// the document for repair or a compatible newer build, without activating it.
func startAwake(ctx context.Context, store awakesvc.Store, host awakesvc.Host, bindings awakesvc.Bindings, log *slog.Logger) (controllers.AwakeService, func()) {
	svc, err := awakesvc.New(ctx, store, host, bindings)
	if err != nil {
		log.Warn("could not initialize Stay Awake; its API will report unimplemented", "error", err)
		// Return a nil interface, not a typed nil service, so controllers return 501.
		return nil, func() {}
	}
	awakeCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer sentryobs.RecoverBackground(); defer close(done); svc.Run(awakeCtx) }()
	return svc, func() { cancel(); <-done }
}
