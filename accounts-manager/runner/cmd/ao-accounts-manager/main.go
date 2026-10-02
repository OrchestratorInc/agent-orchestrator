package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/aoagents/agent-orchestrator/accounts-manager/runner/internal/runner"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return runner.RunCLI(ctx, os.Args[1:], os.Stdout, os.Stderr)
}
