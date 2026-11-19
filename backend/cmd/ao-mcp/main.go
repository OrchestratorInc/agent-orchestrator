// ao-mcp serves Orchestrator's chat tools over stdio MCP without loading the
// ao CLI's commands, database, and services.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/aoagents/agent-orchestrator/backend/internal/mcpserver"
)

func main() {
	if err := mcpserver.Serve(context.Background(), os.Stdin, os.Stdout, mcpserver.NewDaemonClient(), mcpserver.Version); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
