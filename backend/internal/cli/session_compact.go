package cli

import (
	"context"
	"fmt"
	"net/url"

	"github.com/spf13/cobra"
)

type sessionCompactResponse struct {
	TokensBefore int64 `json:"tokensBefore"`
	TokensAfter  int64 `json:"tokensAfter"`
}

// newSessionCompactCommand asks a chat session's agent to summarize its earlier
// history, so later turns stop re-sending all of it.
func newSessionCompactCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "compact <id>",
		Short: "Summarize a chat session's earlier history to reclaim context",
		Long: "Ask a chat session's agent to summarize its earlier history. Every turn re-sends the whole " +
			"conversation, so compacting a long session makes each later turn smaller. The agent does the " +
			"work as its own turn; the result appears on the session's timeline. The session must be idle.",
		Args: oneSessionIDArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := normalizeSessionID(args[0])
			if err != nil {
				return err
			}
			return ctx.compactSession(cmd.Context(), cmd, id)
		},
	}
}

func (c *commandContext) compactSession(ctx context.Context, cmd *cobra.Command, id string) error {
	var res sessionCompactResponse
	if err := c.postJSON(ctx, "sessions/"+url.PathEscape(id)+"/conversation/compact", struct{}{}, &res); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	var err error
	switch {
	case res.TokensBefore > 0 && res.TokensAfter > 0:
		_, err = fmt.Fprintf(out, "compacted %s from %d to %d tokens\n", id, res.TokensBefore, res.TokensAfter)
	case res.TokensBefore > 0:
		_, err = fmt.Fprintf(out, "compacting %s (%d tokens of context); the result will appear on its timeline\n", id, res.TokensBefore)
	default:
		_, err = fmt.Fprintf(out, "compacting %s; the result will appear on its timeline\n", id)
	}
	return err
}
