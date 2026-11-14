package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type sessionSwitchOptions struct {
	agent      string
	model      string
	effort     string
	permission string
	message    string
	force      bool
}

type sessionSwitchResponse struct {
	StoppedTurn             bool     `json:"stoppedTurn"`
	CancelledQueuedMessages int      `json:"cancelledQueuedMessages"`
	FailedPendingApprovals  bool     `json:"failedPendingApprovals"`
	SettingsUpdated         bool     `json:"settingsUpdated"`
	Warnings                []string `json:"warnings"`
}

func (r sessionSwitchResponse) stopped() string {
	var parts []string
	if r.StoppedTurn {
		parts = append(parts, "stopped the running turn")
	}
	switch r.CancelledQueuedMessages {
	case 0:
	case 1:
		parts = append(parts, "cancelled 1 queued message")
	default:
		parts = append(parts, fmt.Sprintf("cancelled %d queued messages", r.CancelledQueuedMessages))
	}
	if r.FailedPendingApprovals {
		parts = append(parts, "declined the pending approval")
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

func (o sessionSwitchOptions) picks() string {
	var parts []string
	for _, pick := range [][2]string{{"model", o.model}, {"effort", o.effort}, {"permission mode", o.permission}} {
		if value := strings.TrimSpace(pick[1]); value != "" {
			parts = append(parts, pick[0]+" "+value)
		}
	}
	return strings.Join(parts, ", ")
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

Switching to the agent already running changes nothing, unless --model,
--effort or --permission name new settings: a Chat agent takes them in place.
A session whose agent exited is started again to hand over.
Run ao agent ls for the agents you can switch to.`,
		Example: `  ao session switch ao-12 --agent codex
  ao session switch ao-12 --agent codex --force --message "Claude is down. Take over."
  ao session ls --all --agent claude-code -q | xargs -n1 ao session switch --agent codex --force`,
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
	f.StringVar(&opts.permission, "permission", "", "Permission mode for the new agent: default (the agent's own config, which for Codex in a terminal means no approvals and no sandbox), accept-edits, auto or bypass-permissions; empty keeps the session's")
	f.StringVarP(&opts.message, "message", "m", "", "Message to send to the new agent after the switch")
	f.BoolVar(&opts.force, "force", false, "Stop the current turn, queued messages and pending approvals, then switch")
	return cmd
}

func (c *commandContext) switchSession(ctx context.Context, cmd *cobra.Command, id string, opts sessionSwitchOptions) error {
	agent, err := knownAgentFlag(opts.agent)
	if err != nil {
		return err
	}
	if agent == "" {
		return usageError{errors.New("usage: --agent is required")}
	}
	switch opts.permission {
	case "", "default", "accept-edits", "auto", "bypass-permissions":
	default:
		return usageError{fmt.Errorf("usage: --permission must be default, accept-edits, auto or bypass-permissions, got %q", opts.permission)}
	}
	out := cmd.OutOrStdout()
	var result sessionSwitchResponse
	err = c.doJSONPathWithHeadersAndTimeout(ctx, http.MethodPost, "/api/v1/sessions/"+url.PathEscape(id)+"/provider-switch", sessionSwitchRequest{
		TargetHarness: agent, IdempotencyKey: uuid.NewString(),
		Model: strings.TrimSpace(opts.model), Effort: strings.TrimSpace(opts.effort),
		PermissionMode: opts.permission, Force: opts.force,
	}, &result, nil, 0)
	var responseErr apiResponseError
	hasResponseErr := errors.As(err, &responseErr)
	switch {
	case err == nil:
		for _, warning := range result.Warnings {
			if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", warning); err != nil {
				return err
			}
		}
		line := fmt.Sprintf("switched %s to %s%s", id, agent, result.stopped())
		if result.SettingsUpdated {
			line = fmt.Sprintf("%s is already on %s; updated %s", id, agent, opts.picks())
		}
		if _, err := fmt.Fprintln(out, line); err != nil {
			return err
		}
	case hasResponseErr && responseErr.ErrorBody.Code == "PROVIDER_SWITCH_already_active":
		if _, err := fmt.Fprintf(out, "%s is already on %s\n", id, agent); err != nil {
			return err
		}
	case hasResponseErr && !opts.force && (responseErr.ErrorBody.Code == "PROVIDER_SWITCH_busy" || responseErr.ErrorBody.Code == "PROVIDER_SWITCH_pending_interaction"):
		return fmt.Errorf("%w; rerun with --force to stop it and switch", err)
	case switchOutcomeUnknown(err):
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

func knownAgentFlag(raw string) (string, error) {
	agent := strings.ToLower(strings.TrimSpace(raw))
	if agent == "" || domain.AgentHarness(agent).IsKnown() {
		return agent, nil
	}
	var near []string
	for _, known := range domain.AllHarnesses {
		if strings.HasPrefix(string(known), agent) {
			near = append(near, string(known))
		}
	}
	if len(near) > 0 {
		return "", usageError{fmt.Errorf("usage: unknown agent %q, did you mean %s? Run ao agent ls for the agents", raw, strings.Join(near, " or "))}
	}
	return "", usageError{fmt.Errorf("usage: unknown agent %q; run ao agent ls for the agents", raw)}
}

func switchOutcomeUnknown(err error) bool {
	var responseErr apiResponseError
	if !errors.As(err, &responseErr) {
		return !errors.Is(err, errDaemonNotRunning)
	}
	if strings.HasPrefix(responseErr.ErrorBody.Code, "PROVIDER_SWITCH_") {
		return false
	}
	switch responseErr.StatusCode {
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusGatewayTimeout:
		return true
	}
	return false
}

func (c *commandContext) sendAfterSwitch(ctx context.Context, id, message string) error {
	return c.retryWhileCode(ctx, []string{"SESSION_COMPOSER_BUSY", "SESSION_STARTUP_PENDING", "CHAT_INTERFACE_TRANSITION"}, func() error {
		return c.sendMessage(ctx, sendOptions{session: id, message: message})
	})
}

func (c *commandContext) retryWhileCode(ctx context.Context, codes []string, send func() error) error {
	deadline := time.Now().Add(switchSendRetryWindow)
	for {
		err := send()
		var responseErr apiResponseError
		if !errors.As(err, &responseErr) || !slices.Contains(codes, responseErr.ErrorBody.Code) ||
			!time.Now().Add(switchSendRetryInterval).Before(deadline) {
			return err
		}
		c.deps.Sleep(switchSendRetryInterval)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !time.Now().Before(deadline) {
			return err
		}
	}
}

const (
	switchSendRetryWindow   = 30 * time.Second
	switchSendRetryInterval = 2 * time.Second
)
