package cli

import (
	"context"
	"errors"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

const maxSummaryCharacters = 200

type summaryAPIRequest struct {
	SessionID string `json:"sessionId"`
	Summary   string `json:"summary"`
}

func newSummaryCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "summary <one-line summary>",
		Short: "Update the kanban card summary for this session",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.summary(cmd.Context(), args)
		},
	}
}

func (c *commandContext) summary(ctx context.Context, args []string) error {
	text := strings.TrimSpace(strings.Join(args, " "))
	if text == "" {
		return usageError{errors.New("usage: summary text is required")}
	}
	if utf8.RuneCountInString(text) > maxSummaryCharacters {
		return usageError{errors.New("usage: summary must be at most 200 characters")}
	}
	sessionID := strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
	if sessionID == "" {
		return usageError{errors.New("usage: AO_SESSION_ID is required")}
	}
	var out struct{ OK bool `json:"ok"` }
	return c.postJSON(ctx, "summary", summaryAPIRequest{SessionID: sessionID, Summary: text}, &out)
}
