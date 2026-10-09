package cli

import (
	"context"
	"errors"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

const maxTitleCharacters = 100

type titleAPIRequest struct {
	SessionID string `json:"sessionId"`
	Title     string `json:"title"`
}

func newTitleCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "title <session title>",
		Short: "Set the kanban card title for this session",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.title(cmd.Context(), args)
		},
	}
}

func (c *commandContext) title(ctx context.Context, args []string) error {
	text := strings.TrimSpace(strings.Join(args, " "))
	if text == "" {
		return usageError{errors.New("usage: title text is required")}
	}
	if utf8.RuneCountInString(text) > maxTitleCharacters {
		return usageError{errors.New("usage: title must be at most 100 characters")}
	}
	sessionID := strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
	if sessionID == "" {
		return usageError{errors.New("usage: AO_SESSION_ID is required")}
	}
	var out struct{ OK bool `json:"ok"` }
	return c.postJSON(ctx, "title", titleAPIRequest{SessionID: sessionID, Title: text}, &out)
}
