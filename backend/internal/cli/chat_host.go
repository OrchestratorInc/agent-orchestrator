package cli

import (
	"errors"

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
			err := persistenthost.RunCommand(cmd.Context(), args)
			if errors.Is(err, persistenthost.ErrCommandUsage) {
				return usageError{err}
			}
			return err
		},
	}
}
