// Command rbhost is the standalone browser service for self-hosted remote hosts.
package main

import (
	"os"

	"github.com/aoagents/agent-orchestrator/cloud/internal/agentbrowser/host"
)

func main() { host.Main(os.Args[1:]) }
