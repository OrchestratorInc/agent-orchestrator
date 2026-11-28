package main

import (
	"os"

	"github.com/aoagents/agent-orchestrator/backend/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		cli.PrintError(os.Stderr, err)
		os.Exit(cli.ExitCode(err))
	}
}
