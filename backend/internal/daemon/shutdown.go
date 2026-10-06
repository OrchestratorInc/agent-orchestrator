package daemon

import (
	"context"
	"os/signal"
	"syscall"
)

func daemonContext() (context.Context, context.CancelFunc, context.CancelFunc) {
	signalCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	// Cancelling workers must not restore default signal handling while resource
	// cleanup is still running. Run defers stopSignals until shutdown finishes.
	ctx, cancelWorkers := context.WithCancel(signalCtx)
	return ctx, cancelWorkers, stopSignals
}
