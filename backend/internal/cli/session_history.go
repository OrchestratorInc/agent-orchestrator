package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type sessionHistoryResponse struct {
	Turns  []domain.HandoffTurn `json:"turns"`
	Total  int                  `json:"total"`
	Offset int                  `json:"offset"`
}

// newSessionHistoryCommand prints a session's turns in full, newest first. It
// exists for the agent that took over a session: its handoff note carries the
// recent turns and points here for anything older.
func newSessionHistoryCommand(ctx *commandContext) *cobra.Command {
	var offset, limit int
	cmd := &cobra.Command{
		Use:   "history [id]",
		Short: "Print a session's turns, newest first (defaults to the current session)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
			if len(args) == 1 {
				id = strings.TrimSpace(args[0])
			}
			if id == "" {
				return usageError{errors.New("session id is required outside an AO session (AO_SESSION_ID is not set)")}
			}
			if offset < 0 || limit < 1 {
				return usageError{errors.New("--offset must be 0 or more and --limit must be 1 or more")}
			}
			return ctx.printSessionHistory(cmd.Context(), cmd, id, offset, limit)
		},
	}
	cmd.Flags().IntVar(&offset, "offset", 0, "Number of newest turns to skip")
	cmd.Flags().IntVar(&limit, "limit", 10, "Turns to print (at most 50)")
	return cmd
}

func (c *commandContext) printSessionHistory(ctx context.Context, cmd *cobra.Command, id string, offset, limit int) error {
	query := url.Values{"offset": {fmt.Sprint(offset)}, "limit": {fmt.Sprint(limit)}}
	var response sessionHistoryResponse
	if err := c.getJSON(ctx, "sessions/"+url.PathEscape(id)+"/history?"+query.Encode(), &response); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if len(response.Turns) == 0 {
		_, err := fmt.Fprintf(out, "no turns at offset %d (session has %d)\n", response.Offset, response.Total)
		return err
	}
	for index, turn := range response.Turns {
		// Turn 1 is the oldest, so a number keeps meaning the same turn as the
		// session grows.
		header := fmt.Sprintf("## Turn %d of %d", response.Total-response.Offset-index, response.Total)
		if !turn.At.IsZero() {
			header += "  " + turn.At.UTC().Format(time.RFC3339)
		}
		if turn.Harness != "" {
			header += "  " + string(turn.Harness)
		}
		if strings.TrimSpace(turn.AgentResponse) == "" {
			turn.AgentResponse = "(no final response recorded)"
		}
		if _, err := fmt.Fprintf(out, "%s\nUser:\n%s\n\nAssistant:\n%s\n\n", header, turn.UserRequest, turn.AgentResponse); err != nil {
			return err
		}
	}
	if next := response.Offset + len(response.Turns); next < response.Total {
		_, err := fmt.Fprintf(out, "%d older turns remain. Run `ao session history %s --offset %d --limit %d` to read them.\n",
			response.Total-next, id, next, limit)
		return err
	}
	return nil
}
