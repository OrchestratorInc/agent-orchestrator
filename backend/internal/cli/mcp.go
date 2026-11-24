package cli

import (
	"github.com/spf13/cobra"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/mcpserver"
)

// `ao mcp` serves mcpserver's tools for an install without the ao-mcp
// executable.
func newMCPCommand(_ *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:    "mcp",
		Short:  "Serve Orchestrator's chat tools over stdio MCP (internal)",
		Hidden: true,
		Args:   noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return mcpserver.Serve(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), mcpserver.NewDaemonClient(), Version)
		},
	}
}
