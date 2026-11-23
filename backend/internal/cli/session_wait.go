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

// ponytail: a new session reads as idle until its agent starts. Within
// startupGrace of creation, idle counts only once a turn has completed. A
// session spawned with no prompt therefore settles when the window ends.
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
	case session.Activity.State == "active":
		return ""
	case session.Activity.State == "waiting_input" || session.Activity.State == "blocked":
		return "needs_input"
	default:
		return session.Activity.State
	}
}

func newSessionWaitCommand(ctx *commandContext) *cobra.Command {
	var (
		timeout, interval time.Duration
		asJSON            bool
	)
	cmd := &cobra.Command{
		Use:   "wait <id>...",
		Short: "Block until a session stops working or asks for an answer",
		Long: `Block until the first of the given sessions stops working, then print which
one and why. One call replaces a loop of ` + "`ao session get`" + ` checks.

Exit codes: 0 the agent finished its turn, exited or was terminated; 5 the
session waits for an answer or an approval; 124 no session settled before
--timeout; 4 refused by the grant; 1 the session failed to start, or any other
failure.`,
		Args: usageArgs(cobra.MinimumNArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if timeout <= 0 || interval <= 0 {
				return usageError{errors.New("--timeout and --interval must be above zero")}
			}
			started := ctx.deps.Now()
			waitCtx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			emit := func(result sessionWaitResult) error {
				result.WaitedSeconds = int(ctx.deps.Now().Sub(started).Seconds())
				if asJSON {
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
				if err := emit(sessionWaitResult{SessionID: args[0], State: "active", TimedOut: true}); err != nil {
					return err
				}
				return exitError{code: exitTimedOut, err: fmt.Errorf("no session settled within %s", timeout)}
			}
			get := func(path string, out any) error {
				err := ctx.getJSON(waitCtx, path, out)
				if err != nil && waitCtx.Err() != nil && cmd.Context().Err() == nil {
					return timedOut()
				}
				return err
			}
			for {
				for _, arg := range args {
					id, err := normalizeSessionID(arg)
					if err != nil {
						return err
					}
					var res sessionResponse
					if err := get("sessions/"+url.PathEscape(id), &res); err != nil {
						return err
					}
					state := settledState(res.Session)
					if state == "idle" && ctx.deps.Now().Sub(res.Session.CreatedAt) < startupGrace {
						var history sessionHistoryResponse
						if err := get("sessions/"+url.PathEscape(id)+"/history?limit=1", &history); err != nil {
							return err
						}
						if history.Total == 0 {
							state = ""
						}
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
					}
					return nil
				}
				remaining := timeout - ctx.deps.Now().Sub(started)
				if remaining <= 0 {
					return timedOut()
				}
				ctx.deps.Sleep(min(interval, remaining))
			}
		},
	}
	f := cmd.Flags()
	f.DurationVar(&timeout, "timeout", 10*time.Minute, "Give up after this long and exit 124")
	f.DurationVar(&interval, "interval", 2*time.Second, "Time between checks")
	f.BoolVar(&asJSON, "json", false, "Output as JSON")
	return cmd
}
