package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/coderagents/coderapi"
)

// newCoderLoginCommand collects the URL the native CLI requires on a fresh
// machine, inside the existing AO login terminal. Coder owns credential entry
// and storage; AO passes only the deployment URL as a separate argument.
func newCoderLoginCommand(ctx *commandContext) *cobra.Command {
	var executable string
	cmd := &cobra.Command{
		Use:    "coder-login",
		Short:  "Sign in to a Coder deployment (internal)",
		Hidden: true,
		Args:   noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			binary, err := resolveLoginExecutable(executable, "coder", ctx.deps.LookPath)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprint(cmd.OutOrStdout(), "Coder deployment URL (for example https://coder.example.com): "); err != nil {
				return err
			}
			// This reader consumes one line without buffering the token input
			// the native login process will need, and preserves a real TTY stdin.
			line, err := readCodexLoginSelection(cmd.InOrStdin())
			if err != nil {
				return fmt.Errorf("read Coder deployment URL: %w", err)
			}
			deployment, err := coderapi.ParseDeploymentURL(line)
			if err != nil {
				return usageError{fmt.Errorf("enter an absolute http or https Coder deployment URL without credentials, query or fragment")}
			}
			if err := ctx.deps.RunInteractiveCommand(cmd.Context(), binary, []string{"login", deployment.String()}, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()); err != nil {
				return fmt.Errorf("coder login failed: %w", err)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&executable, "executable", "", "resolved Coder executable (internal)")
	_ = cmd.Flags().MarkHidden("executable")
	return cmd
}
