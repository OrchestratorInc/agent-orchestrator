package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

type sessionSwitchOptions struct {
	agent      string
	model      string
	effort     string
	permission string
	message    string
	force      bool
}

type sessionSwitchRequest struct {
	TargetHarness  string `json:"targetHarness"`
	IdempotencyKey string `json:"idempotencyKey"`
	Model          string `json:"model,omitempty"`
	Effort         string `json:"effort,omitempty"`
	PermissionMode string `json:"permissionMode,omitempty"`
	Force          bool   `json:"force,omitempty"`
}

func newSessionSwitchCommand(ctx *commandContext) *cobra.Command {
	var opts sessionSwitchOptions
	cmd := &cobra.Command{
		Use:   "switch <id> --agent <agent>",
		Short: "Switch a session to another agent",
		Long: `Switch a session to another agent in place. The session keeps its worktree,
branch and conversation; the new agent starts from a handoff note of the work so
far. Works for Chat and Terminal UI sessions on this machine.

A session whose agent is working or waiting on an approval is refused. --force
stops the current turn, cancels queued messages and pending approvals, and
switches anyway: use it when the agent is stuck, for example during a provider
outage.

--message is sent to the new agent once the switch completes, as ao send would.

Switching to the agent already running succeeds without changing anything.
Run ao agent ls for the agents you can switch to.`,
		Example: `  ao session switch ao-12 --agent codex
  ao session switch ao-12 --agent codex --force --message "Claude is down. Take over."
  ao session ls --agent claude-code -q | xargs -n1 ao session switch --agent codex --force`,
		Args: oneSessionIDArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := normalizeSessionID(args[0])
			if err != nil {
				return err
			}
			return ctx.switchSession(cmd.Context(), cmd, id, opts)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.agent, "agent", "", "Agent to switch to (required), for example codex or claude-code")
	f.StringVar(&opts.model, "model", "", "Model the new agent starts on; empty keeps its default")
	f.StringVar(&opts.effort, "effort", "", "Reasoning effort the new agent starts with; empty keeps its default")
	f.StringVar(&opts.permission, "permission", "", "Permission mode for the new agent: default, accept-edits, auto or bypass-permissions; empty keeps the session's")
	f.StringVarP(&opts.message, "message", "m", "", "Message to send to the new agent after the switch")
	f.BoolVar(&opts.force, "force", false, "Stop the current turn, queued messages and pending approvals, then switch")
	return cmd
}

func (c *commandContext) switchSession(ctx context.Context, cmd *cobra.Command, id string, opts sessionSwitchOptions) error {
	agent := strings.TrimSpace(opts.agent)
	if agent == "" {
		return usageError{errors.New("usage: --agent is required")}
	}
	switch opts.permission {
	case "", "default", "accept-edits", "auto", "bypass-permissions":
	default:
		return usageError{fmt.Errorf("usage: --permission must be default, accept-edits, auto or bypass-permissions, got %q", opts.permission)}
	}
	out := cmd.OutOrStdout()
	err := c.doJSONPathWithHeadersAndTimeout(ctx, http.MethodPost, "/api/v1/sessions/"+url.PathEscape(id)+"/provider-switch", sessionSwitchRequest{
		TargetHarness: agent, IdempotencyKey: uuid.NewString(),
		Model: strings.TrimSpace(opts.model), Effort: strings.TrimSpace(opts.effort),
		PermissionMode: opts.permission, Force: opts.force,
	}, nil, nil, 0)
	var responseErr apiResponseError
	hasResponseErr := errors.As(err, &responseErr)
	switch {
	case err == nil:
		if _, err := fmt.Fprintf(out, "switched %s to %s\n", id, agent); err != nil {
			return err
		}
	case hasResponseErr && responseErr.ErrorBody.Code == "PROVIDER_SWITCH_already_active":
		if _, err := fmt.Fprintf(out, "%s is already on %s\n", id, agent); err != nil {
			return err
		}
	case hasResponseErr && !opts.force && (responseErr.ErrorBody.Code == "PROVIDER_SWITCH_busy" || responseErr.ErrorBody.Code == "PROVIDER_SWITCH_pending_interaction"):
		return fmt.Errorf("%w; rerun with --force to stop it and switch", err)
	case errors.Is(err, errDaemonUnavailable) || !hasResponseErr || responseErr.StatusCode >= 500:
		return fmt.Errorf("%w; the switch may still have completed: check ao session get %s", err, id)
	default:
		return err
	}
	if strings.TrimSpace(opts.message) == "" {
		return nil
	}
	if err := c.sendAfterSwitch(ctx, id, opts.message); err != nil {
		return fmt.Errorf("switched, but the message was not sent: %w", err)
	}
	_, err = fmt.Fprintf(out, "sent message to %s\n", id)
	return err
}

func (c *commandContext) sendAfterSwitch(ctx context.Context, id, message string) error {
	deadline := time.Now().Add(switchSendRetryWindow)
	for {
		err := c.sendMessage(ctx, sendOptions{session: id, message: message})
		var responseErr apiResponseError
		starting := errors.As(err, &responseErr) &&
			(responseErr.ErrorBody.Code == "SESSION_COMPOSER_BUSY" || responseErr.ErrorBody.Code == "SESSION_STARTUP_PENDING")
		if !starting || !time.Now().Add(switchSendRetryInterval).Before(deadline) {
			return err
		}
		c.deps.Sleep(switchSendRetryInterval)
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
}

const (
	switchSendRetryWindow   = 30 * time.Second
	switchSendRetryInterval = 2 * time.Second
)
