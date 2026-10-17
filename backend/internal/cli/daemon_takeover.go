package cli

import (
	"context"
	"errors"
	"time"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
)

// This bootstrap command runs before a daemon is reachable, like daemon/pty-host.
func newDaemonTakeoverCommand() *cobra.Command {
	var expectedSHA256 string
	cmd := &cobra.Command{
		Use: "daemon-takeover <run-file>", Hidden: true,
		Short: "Reclaim a verified orphan daemon (internal)",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if expectedSHA256 == "" {
				return usageError{errors.New("daemon-takeover requires --expected-sha256")}
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 8*time.Second)
			defer cancel()
			return runfile.Takeover(ctx, args[0], expectedSHA256)
		},
	}
	cmd.Flags().StringVar(&expectedSHA256, "expected-sha256", "", "SHA-256 of the observed daemon owner record")
	return cmd
}
