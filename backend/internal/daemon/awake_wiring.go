package daemon

import (
	"context"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/sentryobs"
	awakesvc "github.com/aoagents/agent-orchestrator/backend/internal/service/awake"
)

func startAwake(ctx context.Context, store awakesvc.Store, host awakesvc.Host, bindings awakesvc.Bindings, log *slog.Logger) (controllers.AwakeService, func()) {
	svc, err := awakesvc.New(ctx, store, host, bindings)
	if err != nil {
		log.Warn("could not initialize Stay Awake; its API will report unimplemented", "error", err)
		return nil, func() {}
	}
	awakeCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer sentryobs.RecoverBackground(); defer close(done); svc.Run(awakeCtx) }()
	return svc, func() { cancel(); <-done }
}
