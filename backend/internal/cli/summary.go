package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// summaryTimeout keeps a best-effort card update from stalling the agent that
// runs it. The daemon does one small write, so a call this slow has failed.
const summaryTimeout = 5 * time.Second

type summaryAPIRequest struct {
	SessionID string `json:"sessionId"`
	Summary   string `json:"summary"`
	Done      bool   `json:"done,omitempty"`
}

type summaryAPIResponse struct {
	Updated           bool `json:"updated"`
	RetryAfterSeconds int  `json:"retryAfterSeconds"`
}

func newSummaryCommand(ctx *commandContext) *cobra.Command {
	var done bool
	cmd := &cobra.Command{
		Use:   "summary <one-line summary>",
		Short: "Update the kanban card summary for this session",
		Args:  usageArgs(cobra.MinimumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.summary(cmd.Context(), cmd.OutOrStdout(), args, done)
		},
	}
	cmd.Flags().BoolVar(&done, "done", false, "Set the final summary; it is never held back")
	return cmd
}

func (c *commandContext) summary(ctx context.Context, out io.Writer, args []string, done bool) error {
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
	var resp summaryAPIResponse
	req := summaryAPIRequest{SessionID: sessionID, Summary: text, Done: done}
	if err := c.doJSONPathWithHeadersAndTimeout(ctx, http.MethodPost, "/api/v1/summary", req, &resp, nil, summaryTimeout); err != nil {
		return err
	}
	if !resp.Updated {
		_, err := fmt.Fprintf(out, "Summary not updated: the previous one is too recent. Try again in %s, or carry on.\n",
			time.Duration(resp.RetryAfterSeconds)*time.Second)
		return err
	}
	return nil
}
