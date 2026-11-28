package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type sessionSwitchOptions struct {
	from       string
	model      string
	effort     string
	permission string
	message    string
	force      bool
}

func (o sessionSwitchOptions) picks() string {
	var parts []string
	for _, pick := range [][2]string{{"model", o.model}, {"effort", o.effort}, {"permission mode", o.permission}} {
		if pick[1] != "" {
			parts = append(parts, pick[0]+" "+pick[1])
		}
	}
	return strings.Join(parts, ", ")
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

type sessionSwitchRequest struct {
	TargetHarness  string `json:"targetHarness"`
	IdempotencyKey string `json:"idempotencyKey"`
	Model          string `json:"model,omitempty"`
	Effort         string `json:"effort,omitempty"`
	PermissionMode string `json:"permissionMode,omitempty"`
	Force          bool   `json:"force,omitempty"`
}

const switchFromLimit = 4

func newSessionSwitchCommand(ctx *commandContext) *cobra.Command {
	var opts sessionSwitchOptions
	cmd := &cobra.Command{
		Use:   "switch <id> [agent]",
		Short: "Switch a session to another agent, model, effort or permission mode",
		Long: `Switch a session's agent, model, effort or permission mode in place. The
session keeps its worktree, branch and conversation. Works for Chat and Terminal
UI sessions on this machine, including one whose agent exited.

Name an agent to move the session to it. The new agent starts from a handoff
note of the work so far, on --model and --effort if given and on its defaults
otherwise. Leave the agent out, or name the one already running, to change only
--model, --effort or --permission: a Chat agent takes them from its next turn,
and a Terminal UI agent restarts on its own conversation.

A switch that has to stop the agent is refused while it is working or waiting
on an approval. --force stops the current turn, cancels queued messages and
pending approvals, and switches anyway. On a Chat settings change, --force stops
the current turn so the next one runs on the new settings.

--from <agent> makes the same change to every session on that agent,
orchestrators included, except the agent session running the command. It runs
four at a time and exits 1 if any of them was not switched or did not get the
message.

--message is sent to the agent once the switch completes, as ao send would.
Run ao agent ls for the agents you can switch to.`,
		Example: `  ao session switch ao-12 codex
  ao session switch ao-12 --model opus --effort high
  ao session switch ao-12 codex --model gpt-6.1-sol --effort low --permission accept-edits
  ao session switch --from claude codex --force -m "Claude is down. Take over."`,
		Args: func(_ *cobra.Command, args []string) error {
			low, high := 1, 2
			if opts.from != "" {
				low, high = 0, 1
			}
			if len(args) < low || len(args) > high {
				return usageError{errors.New("usage: ao session switch <id> [agent], or ao session switch --from <agent> [agent]")}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.model, opts.effort, opts.permission = strings.TrimSpace(opts.model), strings.TrimSpace(opts.effort), strings.TrimSpace(opts.permission)
			var agent string
			if (opts.from == "" && len(args) == 2) || (opts.from != "" && len(args) == 1) {
				var err error
				if agent, err = knownAgentFlag(args[len(args)-1]); err != nil {
					return err
				}
			}
			if agent == "" && opts.picks() == "" {
				return usageError{errors.New("usage: name an agent to switch to, or a --model, --effort or --permission to change")}
			}
			switch opts.permission {
			case "", "default", "accept-edits", "auto", "bypass-permissions":
			default:
				return usageError{fmt.Errorf("usage: --permission must be default, accept-edits, auto or bypass-permissions, got %q", opts.permission)}
			}
			if opts.from == "" {
				id, err := normalizeSessionID(args[0])
				if err != nil {
					return err
				}
				if agent == "" {
					var res sessionResponse
					if err := ctx.getJSON(cmd.Context(), "sessions/"+url.PathEscape(id), &res); err != nil {
						return err
					}
					agent = res.Session.Harness
				}
				return ctx.switchSession(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), id, agent, opts)
			}
			from, err := knownAgentFlag(opts.from)
			if err != nil {
				return err
			}
			if agent == "" {
				agent = from
			}
			if from == agent && opts.picks() == "" {
				return usageError{fmt.Errorf("usage: --from %s names the agent being switched to", opts.from)}
			}
			return ctx.switchSessionsFrom(cmd.Context(), cmd, from, agent, opts)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.from, "from", "", "Switch every session on this agent, for example claude")
	f.StringVar(&opts.model, "model", "", "Model to run; empty keeps the current one, or the new agent's default")
	f.StringVar(&opts.effort, "effort", "", "Reasoning effort; empty keeps the current one, or the new agent's default")
	f.StringVar(&opts.permission, "permission", "", "Permission mode: default (the agent's own config), accept-edits, auto or bypass-permissions; empty keeps the session's")
	f.StringVarP(&opts.message, "message", "m", "", "Message to send to the agent after the switch")
	f.BoolVar(&opts.force, "force", false, "Stop the current turn, queued messages and pending approvals, then switch")
	return cmd
}

func (c *commandContext) switchSessionsFrom(ctx context.Context, cmd *cobra.Command, from, agent string, opts sessionSwitchOptions) error {
	var res sessionListResponse
	if err := c.getJSON(ctx, apiPath("sessions", url.Values{"active": {"true"}}), &res); err != nil {
		return err
	}
	self := callerSession()
	var ids []string
	none := "no sessions on %s\n"
	out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()
	for _, sess := range filterAndSortSessions(res.Sessions, true) {
		switch {
		case sess.Harness != from:
		case sess.ID == self:
			none = "no other sessions on %s\n"
			if _, err := fmt.Fprintf(out, "skipped %s, the session running this command\n", self); err != nil {
				return err
			}
		default:
			ids = append(ids, sess.ID)
		}
	}
	if len(ids) == 0 {
		_, err := fmt.Fprintf(out, none, from)
		return err
	}
	var mu sync.Mutex
	failed, unsent := 0, 0
	var group errgroup.Group
	group.SetLimit(switchFromLimit)
	for _, id := range ids {
		group.Go(func() error {
			var lines, warnings bytes.Buffer
			err := c.switchSession(ctx, &lines, &warnings, id, agent, opts)
			mu.Lock()
			defer mu.Unlock()
			_, _ = out.Write(lines.Bytes())
			_, _ = errOut.Write(warnings.Bytes())
			if errors.Is(err, errMessageNotSent) {
				unsent++
			} else if err != nil {
				failed++
			}
			if err != nil {
				_, _ = fmt.Fprintf(errOut, "%s: %v\n", id, err)
			}
			return nil
		})
	}
	_ = group.Wait()
	done := fmt.Sprintf("switched %d sessions from %s to %s", len(ids), from, agent)
	if from == agent {
		done = fmt.Sprintf("applied %s to %d sessions on %s", opts.picks(), len(ids), from)
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d sessions on %s were not switched", failed, len(ids), from)
	}
	if unsent > 0 {
		return fmt.Errorf("%s, but %d did not get the message", done, unsent)
	}
	_, err := fmt.Fprintln(out, done)
	return err
}

func (c *commandContext) switchSession(ctx context.Context, out, errOut io.Writer, id, agent string, opts sessionSwitchOptions) error {
	var result sessionSwitchResponse
	err := c.doJSONPathWithHeadersAndTimeout(ctx, http.MethodPost, "/api/v1/sessions/"+url.PathEscape(id)+"/provider-switch", sessionSwitchRequest{
		TargetHarness: agent, IdempotencyKey: uuid.NewString(),
		Model: opts.model, Effort: opts.effort,
		PermissionMode: opts.permission, Force: opts.force,
	}, &result, nil, 0)
	var responseErr apiResponseError
	hasResponseErr := errors.As(err, &responseErr)
	picks := opts.picks()
	switch {
	case err == nil:
		for _, warning := range result.Warnings {
			if _, err := fmt.Fprintf(errOut, "warning: %s\n", warning); err != nil {
				return err
			}
		}
		line := fmt.Sprintf("switched %s to %s", id, agent)
		if result.SettingsUpdated {
			line = fmt.Sprintf("updated %s on %s: %s", id, agent, picks)
		} else if picks != "" {
			line += " with " + picks
		}
		if _, err := fmt.Fprintln(out, line+result.stopped()); err != nil {
			return err
		}
	case hasResponseErr && responseErr.ErrorBody.Code == "PROVIDER_SWITCH_already_active":
		line := fmt.Sprintf("%s is already on %s", id, agent)
		if picks != "" {
			line += " with " + picks
		}
		if _, err := fmt.Fprintln(out, line); err != nil {
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
		return fmt.Errorf("%w: %w", errMessageNotSent, err)
	}
	_, err = fmt.Fprintf(out, "sent message to %s\n", id)
	return err
}

var errMessageNotSent = errors.New("switched, but the message was not sent")

func knownAgentFlag(raw string) (string, error) {
	agent := strings.ToLower(strings.TrimSpace(raw))
	if agent == "claude" {
		agent = string(domain.HarnessClaudeCode)
	}
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
