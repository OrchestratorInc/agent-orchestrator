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
	from    string
	message string
	force   bool
}

type sessionSwitchResponse struct {
	StoppedTurn             bool `json:"stoppedTurn"`
	CancelledQueuedMessages int  `json:"cancelledQueuedMessages"`
	FailedPendingApprovals  bool `json:"failedPendingApprovals"`
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
	Force          bool   `json:"force,omitempty"`
}

const switchFromLimit = 4

func newSessionSwitchCommand(ctx *commandContext) *cobra.Command {
	var opts sessionSwitchOptions
	cmd := &cobra.Command{
		Use:   "switch <id> <agent>",
		Short: "Switch a session to another agent",
		Long: `Switch a session to another agent in place. The session keeps its worktree,
branch and conversation; the new agent starts on its default model from a
handoff note of the work so far. Works for Chat and Terminal UI sessions on this
machine, including one whose agent exited.

A session whose agent is working or waiting on an approval is refused. --force
stops the current turn, cancels queued messages and pending approvals, and
switches anyway: use it when the agent is stuck, for example during a provider
outage.

--from <agent> switches every session on that agent instead of one,
orchestrators included, four at a time, and exits 1 if any of them was not
switched or did not get the message.

--message is sent to the new agent once the switch completes, as ao send would.
Run ao agent ls for the agents you can switch to.`,
		Example: `  ao session switch ao-12 codex
  ao session switch ao-12 codex --force -m "Claude is down. Take over."
  ao session switch --from claude codex --force -m "Claude is down. Take over."`,
		Args: func(_ *cobra.Command, args []string) error {
			want := 2
			if opts.from != "" {
				want = 1
			}
			if len(args) != want {
				return usageError{errors.New("usage: ao session switch <id> <agent>, or ao session switch --from <agent> <agent>")}
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			agent, err := knownAgentFlag(args[len(args)-1])
			if err != nil {
				return err
			}
			if agent == "" {
				return usageError{errors.New("usage: name the agent to switch to")}
			}
			if opts.from == "" {
				id, err := normalizeSessionID(args[0])
				if err != nil {
					return err
				}
				return ctx.switchSession(cmd.Context(), cmd.OutOrStdout(), id, agent, opts)
			}
			from, err := knownAgentFlag(opts.from)
			if err != nil {
				return err
			}
			if from == agent {
				return usageError{fmt.Errorf("usage: --from %s names the agent being switched to", opts.from)}
			}
			return ctx.switchSessionsFrom(cmd.Context(), cmd, from, agent, opts)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.from, "from", "", "Switch every session on this agent, for example claude")
	f.StringVarP(&opts.message, "message", "m", "", "Message to send to the new agent after the switch")
	f.BoolVar(&opts.force, "force", false, "Stop the current turn, queued messages and pending approvals, then switch")
	return cmd
}

func (c *commandContext) switchSessionsFrom(ctx context.Context, cmd *cobra.Command, from, agent string, opts sessionSwitchOptions) error {
	var res sessionListResponse
	if err := c.getJSON(ctx, apiPath("sessions", url.Values{"active": {"true"}}), &res); err != nil {
		return err
	}
	var ids []string
	for _, sess := range filterAndSortSessions(res.Sessions, true) {
		if sess.Harness == from {
			ids = append(ids, sess.ID)
		}
	}
	out := cmd.OutOrStdout()
	if len(ids) == 0 {
		_, err := fmt.Fprintf(out, "no sessions on %s\n", from)
		return err
	}
	var mu sync.Mutex
	failed, unsent := 0, 0
	var group errgroup.Group
	group.SetLimit(switchFromLimit)
	for _, id := range ids {
		group.Go(func() error {
			var lines bytes.Buffer
			err := c.switchSession(ctx, &lines, id, agent, opts)
			mu.Lock()
			defer mu.Unlock()
			_, _ = out.Write(lines.Bytes())
			if errors.Is(err, errMessageNotSent) {
				unsent++
			} else if err != nil {
				failed++
			}
			if err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s: %v\n", id, err)
			}
			return nil
		})
	}
	_ = group.Wait()
	if failed > 0 {
		return fmt.Errorf("%d of %d sessions on %s were not switched", failed, len(ids), from)
	}
	if unsent > 0 {
		return fmt.Errorf("switched %d sessions from %s to %s, but %d did not get the message", len(ids), from, agent, unsent)
	}
	_, err := fmt.Fprintf(out, "switched %d sessions from %s to %s\n", len(ids), from, agent)
	return err
}

func (c *commandContext) switchSession(ctx context.Context, out io.Writer, id, agent string, opts sessionSwitchOptions) error {
	var result sessionSwitchResponse
	err := c.doJSONPathWithHeadersAndTimeout(ctx, http.MethodPost, "/api/v1/sessions/"+url.PathEscape(id)+"/provider-switch", sessionSwitchRequest{
		TargetHarness: agent, IdempotencyKey: uuid.NewString(), Force: opts.force,
	}, &result, nil, 0)
	var responseErr apiResponseError
	hasResponseErr := errors.As(err, &responseErr)
	switch {
	case err == nil:
		if _, err := fmt.Fprintf(out, "switched %s to %s%s\n", id, agent, result.stopped()); err != nil {
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
