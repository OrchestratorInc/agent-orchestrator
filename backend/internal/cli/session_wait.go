package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/spf13/cobra"
)

type sessionWaitResult struct {
	SessionID     string `json:"sessionId"`
	State         string `json:"state"`
	Status        string `json:"status,omitempty"`
	WaitedSeconds int    `json:"waitedSeconds"`
	TimedOut      bool   `json:"timedOut,omitempty"`
}

type sessionWaitOptions struct {
	timeout, interval time.Duration
	json              bool
	after             map[string]time.Time
}

const startupGrace = time.Minute

func settledState(session sessionDTO) string {
	switch {
	case session.IsTerminated:
		return "terminated"
	case session.ProvisionState != "" && session.ProvisionState != "ready":
		if session.ProvisionState == "failed" {
			return "failed"
		}
		return ""
	case session.StatusReadiness == "unavailable":
		return "unverified"
	case session.StatusReadiness != "" && session.StatusReadiness != "ready":
		return ""
	case session.Activity.State == "active":
		return ""
	case session.Activity.State == "waiting_input" || session.Activity.State == "blocked":
		return "needs_input"
	default:
		return session.Activity.State
	}
}

func newSessionWaitCommand(ctx *commandContext) *cobra.Command {
	var opts sessionWaitOptions
	cmd := &cobra.Command{
		Use:   "wait <id>...",
		Short: "Block until a session stops working or asks for an answer",
		Long: `Block until the first of the given sessions stops working, then print which
one and why. One call replaces a loop of ` + "`ao session get`" + ` checks.

A session that is idle when the command starts settles at once. To message a
session and wait for its reply, use ` + "`ao send --wait`" + `: a message can reach
the agent after ` + "`ao send`" + ` returns, so a wait started then can see the
idle state from before the message.

Exit codes: 0 the agent finished its turn, exited or was terminated; 5 the
session waits for an answer or an approval; 124 no session settled before
--timeout; 4 refused by the grant; 1 the session failed to start, the daemon
could not verify it after a restart, or any other failure.`,
		Args: usageArgs(cobra.MinimumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.waitForSessions(cmd, args, opts)
		},
	}
	f := cmd.Flags()
	f.DurationVar(&opts.timeout, "timeout", 10*time.Minute, "Give up after this long and exit 124")
	f.DurationVar(&opts.interval, "interval", 2*time.Second, "Time between checks")
	f.BoolVar(&opts.json, "json", false, "Output as JSON")
	return cmd
}

func (c *commandContext) waitForSessions(cmd *cobra.Command, ids []string, opts sessionWaitOptions) error {
	if opts.timeout <= 0 || opts.interval <= 0 {
		return usageError{errors.New("--timeout and --interval must be above zero")}
	}
	started := c.deps.Now()
	waitCtx, cancel := context.WithTimeout(cmd.Context(), opts.timeout)
	defer cancel()
	emit := func(result sessionWaitResult) error {
		result.WaitedSeconds = int(c.deps.Now().Sub(started).Seconds())
		if opts.json {
			return writeJSON(cmd.OutOrStdout(), result)
		}
		state := result.State
		if result.TimedOut {
			state = "still working"
		}
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s: %s after %ds\n", result.SessionID, state, result.WaitedSeconds)
		return err
	}
	timedOut := func() error {
		if err := emit(sessionWaitResult{SessionID: ids[0], State: "active", TimedOut: true}); err != nil {
			return err
		}
		return exitError{code: exitTimedOut, err: fmt.Errorf("no session settled within %s", opts.timeout)}
	}
	get := func(path string, out any) error {
		err := c.getJSON(waitCtx, path, out)
		if err != nil && waitCtx.Err() != nil && cmd.Context().Err() == nil {
			return timedOut()
		}
		return err
	}
	for {
		for _, arg := range ids {
			id, err := normalizeSessionID(arg)
			if err != nil {
				return err
			}
			var res sessionResponse
			if err := get("sessions/"+url.PathEscape(id), &res); err != nil {
				return err
			}
			state := settledState(res.Session)
			if state == "idle" && c.deps.Now().Sub(res.Session.CreatedAt) < startupGrace {
				var history sessionHistoryResponse
				if err := get("sessions/"+url.PathEscape(id)+"/history?limit=1", &history); err != nil {
					return err
				}
				if history.Total == 0 {
					state = ""
				}
			}
			if before, sent := opts.after[id]; sent && (state == "idle" || state == "needs_input" || state == "unverified") && !res.Session.Activity.LastActivityAt.After(before) {
				state = ""
			}
			if state == "" {
				continue
			}
			if err := emit(sessionWaitResult{SessionID: id, State: state, Status: res.Session.Status}); err != nil {
				return err
			}
			switch state {
			case "needs_input":
				return exitError{code: exitNeedsInput, err: fmt.Errorf("%s waits for an answer", id)}
			case "failed":
				return fmt.Errorf("%s failed to start", id)
			case "unverified":
				return fmt.Errorf("the daemon could not verify %s after it restarted, so its recorded state may be stale", id)
			}
			return nil
		}
		remaining := opts.timeout - c.deps.Now().Sub(started)
		if remaining <= 0 {
			return timedOut()
		}
		c.deps.Sleep(min(opts.interval, remaining))
	}
}
