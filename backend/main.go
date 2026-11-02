// Command backend is a compatibility wrapper for the Agent Orchestrator daemon.
// The user-facing CLI lives at cmd/ao; keep this wrapper so existing `go run .`
// development workflows continue to start the daemon while scripts migrate.
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
