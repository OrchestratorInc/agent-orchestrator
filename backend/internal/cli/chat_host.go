package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
)

func newChatHostCommand() *cobra.Command {
	return &cobra.Command{
		Use:                "chat-host",
		Short:              "Run a persistent Chat provider host (internal)",
		Hidden:             true,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := persistenthost.ParseArgs(args)
			if err != nil {
				return usageError{err}
			}
			cfg.Env = os.Environ()
			return persistenthost.Run(cmd.Context(), cfg)
		},
	}
}
