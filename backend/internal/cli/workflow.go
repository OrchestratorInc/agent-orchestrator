package cli

import (
	"github.com/spf13/cobra"
)

// newWorkflowCommand builds `ao workflow`, the operator-facing entry point for the durable
// workflow surfaces.
//
// Only the operator verbs live here today: the worker half of the workflow API is reached by an
// agent through its MCP tools, which derive the caller's identity from the environment AO set on
// the session. Putting those here would give a shell a way to speak as an attempt, which is
// exactly the separation the principal resolver enforces.
func newWorkflowCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workflow",
		Short: "Durable workflow operations",
		Long: "Durable workflow operations.\n\n" +
			"`ao workflow operator <verb>` performs the operator-only actions (bind, decide, resolve, " +
			"reconcile, resume) after a human approves them in the desktop app.",
	}
	cmd.AddCommand(newWorkflowOperatorCommand(ctx))
	return cmd
}
