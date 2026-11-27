package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

// maxSendKeepLen mirrors the daemon's limit on --keep.
const maxSendKeepLen = 1000

type sendOptions struct {
	session         string
	message         string
	steer           bool
	clientMessageID string
	recoverOnly     bool
	task            bool
	keep            string
}

// sendAPIRequest mirrors the daemon's SendSessionMessageRequest body for
// POST /api/v1/sessions/{id}/send. The CLI keeps its own copy so it need not
// import httpd.
type sendAPIRequest struct {
	UserAuthored    bool   `json:"userAuthored,omitempty"`
	SenderSessionID string `json:"senderSessionId,omitempty"`
	Message         string `json:"message"`
	Task            bool   `json:"task,omitempty"`
	Keep            string `json:"keep,omitempty"`
}

// The steering DTOs mirror the daemon conversation API without coupling the
// thin CLI to the HTTP controller package.
type conversationMessageAPIRequest struct {
	Text            string `json:"text"`
	ClientMessageID string `json:"clientMessageId"`
	SenderSessionID string `json:"senderSessionId,omitempty"`
	RecoverOnly     bool   `json:"recoverOnly,omitempty"`
}

type steerOrSendAPIResponse struct {
	Outcome        string `json:"outcome"`
	TurnID         string `json:"turnId"`
	ProviderTurnID string `json:"providerTurnId"`
	ActivityID     string `json:"activityId"`
	State          string `json:"state"`
	Duplicate      bool   `json:"duplicate"`
}

func newSendCommand(ctx *commandContext) *cobra.Command {
	var opts sendOptions
	cmd := &cobra.Command{
		Use:   "send",
		Short: "Send a message to a running agent session",
		Long: "Send a message to a running agent session.\n\n" +
			"This messages agents, not shell terminals. To type a command into one of\n" +
			"this session's terminal tabs, use `ao pane run <tab> <command>` or\n" +
			"`ao pane send <tab> <text>`; `ao pane` lists the terminal tab keys.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return ctx.sendMessage(cmd.Context(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.session, "session", "", "Session id (required)")
	cmd.Flags().StringVar(&opts.message, "message", "", "Message body (required unless --recover-only)")
	cmd.Flags().BoolVar(&opts.steer, "steer", false, "Steer an active Chat turn, or start the next turn when idle")
	cmd.Flags().StringVar(&opts.clientMessageID, "client-message-id", "", "Stable steering delivery handle")
	cmd.Flags().BoolVar(&opts.recoverOnly, "recover-only", false, "Recover a steering result without contacting the provider")
	cmd.Flags().BoolVar(&opts.task, "task", false, "Mark the message as a new task, not a note about the current work; only a new task can compact a worker first")
	cmd.Flags().StringVar(&opts.keep, "keep", "", "What the new task needs from the session's earlier work, kept by that compaction (requires --task)")
	return cmd
}

func (c *commandContext) sendMessage(ctx context.Context, opts sendOptions) error {
	if opts.recoverOnly && !opts.steer {
		return usageError{errors.New("usage: --recover-only requires --steer")}
	}
	if strings.TrimSpace(opts.clientMessageID) != "" && !opts.steer {
		return usageError{errors.New("usage: --client-message-id requires --steer")}
	}
	if opts.recoverOnly && strings.TrimSpace(opts.clientMessageID) == "" {
		return usageError{errors.New("usage: --recover-only requires --client-message-id")}
	}
	if !opts.recoverOnly && strings.TrimSpace(opts.message) == "" {
		return usageError{errors.New("usage: --message is required")}
	}
	if opts.task && opts.steer {
		return usageError{errors.New("usage: --task cannot be combined with --steer; a steer joins the current turn")}
	}
	keep := strings.TrimSpace(opts.keep)
	if keep != "" && !opts.task {
		return usageError{errors.New("usage: --keep requires --task")}
	}
	if utf8.RuneCountInString(keep) > maxSendKeepLen {
		return usageError{fmt.Errorf("usage: --keep must be %d characters or fewer", maxSendKeepLen)}
	}
	message := opts.message
	if sender := strings.TrimSpace(os.Getenv("AO_SESSION_ID")); !opts.recoverOnly && sender != "" {
		message = "[from " + sender + "] " + message
	}
	session := strings.TrimSpace(opts.session)
	if session == "" {
		return usageError{errors.New("usage: --session is required")}
	}

	// PathEscape: session ids are already "-"/digit safe, but may later come
	// from sanitized issue refs; keep the URL well-formed regardless.
	path := "sessions/" + url.PathEscape(session)
	if !opts.steer {
		return c.retryWhileCode(ctx, []string{"CHAT_INTERFACE_TRANSITION"}, func() error {
			return c.postJSON(ctx, path+"/send", sendAPIRequest{
				Message: message, SenderSessionID: strings.TrimSpace(os.Getenv("AO_SESSION_ID")),
				UserAuthored: strings.TrimSpace(os.Getenv("AO_SESSION_ID")) == "", Task: opts.task, Keep: keep,
			}, nil)
		})
	}
	return c.steerMessage(ctx, path, message, strings.TrimSpace(opts.clientMessageID), opts.recoverOnly)
}

func (c *commandContext) steerMessage(
	ctx context.Context,
	sessionPath, message, clientMessageID string,
	recoverOnly bool,
) error {
	if clientMessageID == "" {
		clientMessageID = uuid.NewString()
	}
	var result steerOrSendAPIResponse
	err := c.postJSON(ctx, sessionPath+"/conversation/steer-or-send", conversationMessageAPIRequest{
		Text: message, ClientMessageID: clientMessageID, SenderSessionID: strings.TrimSpace(os.Getenv("AO_SESSION_ID")), RecoverOnly: recoverOnly,
	}, &result)
	if err != nil {
		var responseErr apiResponseError
		hasResponseErr := errors.As(err, &responseErr)
		if hasResponseErr && responseErr.ErrorBody.Code == "CHAT_STEER_UNCERTAIN" {
			if recoverOnly {
				return fmt.Errorf("%w; delivery handle %s remains unresolved and was not resent", err, clientMessageID)
			}
			return fmt.Errorf(
				"%w; recover this delivery without resending it: ao send --session %s --steer --recover-only --client-message-id %s",
				err, strings.TrimPrefix(sessionPath, "sessions/"), clientMessageID)
		}
		if errors.Is(err, errDaemonUnavailable) || !hasResponseErr || responseErr.StatusCode >= 500 {
			return fmt.Errorf(
				"%w; outcome is unknown for delivery handle %s; retry safely with --client-message-id %s",
				err, clientMessageID, clientMessageID)
		}
		return err
	}

	if result.Outcome == "steered" {
		if recoverOnly {
			_, _ = fmt.Fprintf(c.deps.Out,
				"Recovered steering receipt for turn %s with delivery handle %s; agent action is not confirmed.\n",
				result.ProviderTurnID, clientMessageID)
			return nil
		}
		_, _ = fmt.Fprintf(c.deps.Out,
			"Steering accepted by provider for active turn %s with delivery handle %s; agent action is not confirmed.\n",
			result.ProviderTurnID, clientMessageID)
		return nil
	}
	if result.Duplicate {
		_, _ = fmt.Fprintln(c.deps.Out, "Message was already accepted; delivery state is unchanged.")
		return nil
	}
	_, _ = fmt.Fprintf(c.deps.Out,
		"No active turn to steer. Message accepted as a normal Chat turn in %s state with delivery handle %s; provider delivery is not confirmed.\n",
		result.State, clientMessageID)
	return nil
}
