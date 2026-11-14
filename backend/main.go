package main

import (
	"fmt"
	"os"

	"github.com/aoagents/agent-orchestrator/backend/internal/daemon"
)

func main() {
	if err := daemon.Run(""); err != nil {
		fmt.Fprintln(os.Stderr, "ao backend daemon: "+err.Error())
		os.Exit(1)
	}
}
