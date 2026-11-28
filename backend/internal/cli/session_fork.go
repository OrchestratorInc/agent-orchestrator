package cli

import (
	"context"
	"fmt"
	"net/url"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

type sessionForkRequest struct {
	ClientRequestID string `json:"clientRequestId"`
}

type sessionForkResponse struct {
	SessionID   string `json:"sessionId"`
	DisplayName string `json:"displayName"`
}

type sessionForkOptionsResponse struct {
	Allowed       bool   `json:"allowed"`
	SuggestedName string `json:"suggestedName"`
	Refusal       *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"refusal"`
}

// newSessionForkCommand forks a session at its latest finished turn into a
// new session with its own worktree.
func newSessionForkCommand(ctx *commandContext) *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "fork <id>",
		Short: "Fork a session into a new one",
		Long: "Fork a session at its latest finished turn into a new session with its own worktree and branch, " +
			"its current files, and the agent's own memory of the conversation.",
		Args: oneSessionIDArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := normalizeSessionID(args[0])
			if err != nil {
				return err
			}
			if check {
				return ctx.checkSessionFork(cmd.Context(), cmd, id)
			}
			return ctx.forkSession(cmd.Context(), cmd, id)
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "Only report whether the session can be forked")
	return cmd
}

func (c *commandContext) forkSession(ctx context.Context, cmd *cobra.Command, id string) error {
	var res sessionForkResponse
	if err := c.postJSON(ctx, "sessions/"+url.PathEscape(id)+"/fork", sessionForkRequest{ClientRequestID: uuid.NewString()}, &res); err != nil {
		return err
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "forked %s into %s %q\n", id, res.SessionID, res.DisplayName)
	return err
}

func (c *commandContext) checkSessionFork(ctx context.Context, cmd *cobra.Command, id string) error {
	var res sessionForkOptionsResponse
	if err := c.getJSON(ctx, "sessions/"+url.PathEscape(id)+"/fork-options", &res); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if res.Allowed {
		_, err := fmt.Fprintf(out, "%s can be forked as %q\n", id, res.SuggestedName)
		return err
	}
	if res.Refusal != nil {
		_, err := fmt.Fprintf(out, "%s can't be forked: %s (%s)\n", id, res.Refusal.Message, res.Refusal.Code)
		return err
	}
	_, err := fmt.Fprintf(out, "%s can't be forked\n", id)
	return err
}
