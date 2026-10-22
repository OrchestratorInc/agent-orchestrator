package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type summaryAPIRequest struct {
	SessionID string `json:"sessionId"`
	Summary   string `json:"summary"`
}

func newSummaryCommand(ctx *commandContext) *cobra.Command {
	return &cobra.Command{
		Use:   "summary <one-line summary>",
		Short: "Update the kanban card summary for this session",
		Args:  usageArgs(cobra.MinimumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.summary(cmd.Context(), args)
		},
	}
}

func (c *commandContext) summary(ctx context.Context, args []string) error {
	raw := strings.Join(args, " ")
	if strings.TrimSpace(raw) == "" {
		return usageError{errors.New("usage: summary text is required")}
	}
	text := domain.NormalizeCardSummary(raw)
	if text == "" {
		return usageError{fmt.Errorf("usage: summary must be at most %d characters of plain text", domain.MaxCardSummaryRunes)}
	}
	sessionID := strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
	if sessionID == "" {
		return usageError{errors.New("usage: AO_SESSION_ID is required")}
	}
	var out struct {
		OK bool `json:"ok"`
	}
	return c.postJSON(ctx, "summary", summaryAPIRequest{SessionID: sessionID, Summary: text}, &out)
}
