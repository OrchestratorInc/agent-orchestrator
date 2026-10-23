package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// restartAllOptions collects the flags of `ao session restart-all`.
type restartAllOptions struct {
	project              string
	json                 bool
	includeOrchestrators bool
	orchestratorsOnly    bool
	exclude              []string
	self                 string
	dryRun               bool
	yes                  bool
	settleDelay          time.Duration
}

// restartOutcome is the per-session result of a kill+restore round trip.
type restartOutcome struct {
	SessionID string `json:"sessionId"`
	ProjectID string `json:"projectId"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	Detail    string `json:"detail,omitempty"`
}

const (
	restartStatusRestarted     = "restarted"
	restartStatusPreserved     = "restarted (workspace preserved)"
	restartStatusFailed        = "failed"
	restartStatusPlanned       = "planned"
	restartStatusSkippedExited = "skipped (exited before restart)"
)

type restartAllOutput struct {
	Data []restartOutcome `json:"data"`
	Meta struct {
		Total     int `json:"total"`
		Restarted int `json:"restarted"`
		Skipped   int `json:"skipped"`
		Failed    int `json:"failed"`
	} `json:"meta"`
}

func newSessionRestartAllCommand(ctx *commandContext) *cobra.Command {
	var opts restartAllOptions
	cmd := &cobra.Command{
		Use:   "restart-all",
		Short: "Kill and restore every live session so it relaunches on the current agent binary",
		Long: `Kill and restore every live session in sequence.

A running agent holds the inode of its binary, so a new agent-CLI build is only
picked up by a fresh exec. This command performs the supported kill+restore round
trip for each live session, which relaunches it through the same restore path used
at daemon boot (native --resume where a transcript exists, so history is kept).

Workers are restarted by default. Orchestrators are skipped unless
--include-orchestrators (both) or --orchestrators-only (just them) is passed, since
killing an orchestrator interrupts the work it is coordinating.

Sessions whose agent has already finished (activity state "exited") are skipped
too, both at selection time and again right before each kill — restarting a
finished session would relaunch its agent and duplicate work it already did.

The invoking session is always excluded: killing it would terminate this very
process mid-run. It is detected through AO_SESSION_ID, or --self when unset.
A mutating run (i.e. not --dry-run) refuses to proceed if neither is available —
pass --self - to explicitly acknowledge that no session needs protecting.

A mutating run without --yes prompts for confirmation, including under --json:
JSON output does not imply non-interactive authorization, so a non-interactive
--json call must also pass --yes.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.restartAllSessions(cmd.Context(), cmd, opts)
		},
	}
	flags := cmd.Flags()
	addSessionProjectFlag(flags, &opts.project, "Restart only sessions of this project")
	flags.BoolVar(&opts.json, "json", false, "Emit machine-readable JSON")
	flags.BoolVar(&opts.includeOrchestrators, "include-orchestrators", false,
		"Also restart orchestrator sessions (skipped by default)")
	flags.BoolVar(&opts.orchestratorsOnly, "orchestrators-only", false,
		"Restart only orchestrator sessions")
	flags.StringSliceVar(&opts.exclude, "exclude", nil,
		"Session ids to leave running (repeatable, or comma-separated)")
	flags.StringVar(&opts.self, "self", "",
		"Id of the session running this command; excluded from the restart (default: AO_SESSION_ID). "+
			"Pass - to explicitly acknowledge that no session needs protecting (e.g. run from outside AO)")
	flags.BoolVar(&opts.dryRun, "dry-run", false, "List what would be restarted and exit")
	flags.BoolVarP(&opts.yes, "yes", "y", false, "Do not ask for confirmation")
	flags.DurationVar(&opts.settleDelay, "settle-delay", 2*time.Second,
		"Pause between killing a session and restoring it")
	return cmd
}

func (c *commandContext) restartAllSessions(ctx context.Context, cmd *cobra.Command, opts restartAllOptions) error {
	if opts.includeOrchestrators && opts.orchestratorsOnly {
		return usageError{errors.New("--include-orchestrators and --orchestrators-only are mutually exclusive")}
	}

	targets, self, err := c.restartAllTargets(ctx, opts)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	if len(targets) == 0 {
		if opts.json {
			return writeJSON(out, restartAllOutput{Data: []restartOutcome{}})
		}
		_, err := fmt.Fprintln(out, "no live sessions match the selection")
		return err
	}

	// Without a known self-id we cannot guarantee this very session stays out of
	// the list. Warn about it for a dry run (nothing is at stake yet), but for a
	// mutating run fail closed instead of silently risking a self-kill — this is
	// the one case a fail-open warning cannot protect against under --json, where
	// there is no interactive confirmation step to catch it either.
	if self == "" {
		if opts.dryRun {
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
				"warning: AO_SESSION_ID is not set, so the calling session cannot be identified.\n"+
					"         If you are running inside an AO session, pass --self <id> (or --exclude <id>)\n"+
					"         to keep it from being killed mid-run.")
		} else {
			return usageError{errors.New(
				"cannot identify the calling session: AO_SESSION_ID is not set and --self was not passed.\n" +
					"If you are running this from inside an AO session, pass --self <id> (or set AO_SESSION_ID).\n" +
					"If you are running it from an external shell where no session needs protecting, pass --self - to acknowledge that explicitly")}
		}
	}

	if opts.dryRun {
		return writeRestartPlan(cmd, targets, opts.json)
	}

	// Confirmation is gated on --yes alone, matching every other destructive
	// command in this package (project rm, session cleanup) — --json changes the
	// output format, not whether the operation is authorized. A non-interactive
	// --json caller must still pass --yes explicitly.
	if !opts.yes {
		if opts.json {
			return usageError{errors.New("--json requires --yes for a mutating restart-all run (no interactive confirmation is possible)")}
		}
		ok, err := confirmRestart(cmd, targets)
		if err != nil {
			return err
		}
		if !ok {
			_, err := fmt.Fprintln(out, "aborted")
			return err
		}
	}

	results := c.runRestartAll(ctx, cmd, targets, opts)
	return writeRestartResults(cmd, results, opts.json)
}

// restartAllTargets resolves the live sessions selected by opts, in a stable order.
func (c *commandContext) restartAllTargets(ctx context.Context, opts restartAllOptions) ([]sessionDTO, string, error) {
	params := url.Values{}
	if opts.project != "" {
		params.Set("project", opts.project)
	}
	params.Set("active", "true")

	var res sessionListResponse
	if err := c.getJSON(ctx, apiPath("sessions", params), &res); err != nil {
		return nil, "", err
	}

	excluded := make(map[string]struct{}, len(opts.exclude)+1)
	for _, raw := range opts.exclude {
		if id := strings.TrimSpace(raw); id != "" {
			excluded[id] = struct{}{}
		}
	}
	// Never restart the session this command runs in: the kill would take down
	// the very process issuing the restore that follows it. "-" is the explicit
	// "no session to protect, I'm running this from outside AO" acknowledgment;
	// it counts as identified but excludes nothing.
	self := strings.TrimSpace(opts.self)
	if self == "" {
		self = strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
	}
	if self != "" && self != "-" {
		excluded[self] = struct{}{}
	}

	targets := make([]sessionDTO, 0, len(res.Sessions))
	for _, sess := range res.Sessions {
		if sess.IsTerminated {
			continue
		}
		// A session can be non-terminated but already exited (the agent process
		// finished; the session record and transcript are still live — this is
		// exactly what ResumeAgent exists for). Killing it would mark it
		// terminated, and the subsequent restore would relaunch the agent from
		// its last checkpoint, silently duplicating work it already completed.
		if sess.Activity.State == string(domain.ActivityExited) {
			continue
		}
		if _, skip := excluded[sess.ID]; skip {
			continue
		}
		isOrchestrator := sess.Kind == "orchestrator"
		switch {
		case opts.orchestratorsOnly && !isOrchestrator:
			continue
		case !opts.orchestratorsOnly && !opts.includeOrchestrators && isOrchestrator:
			continue
		}
		targets = append(targets, sess)
	}

	sort.Slice(targets, func(i, j int) bool {
		if targets[i].ProjectID != targets[j].ProjectID {
			return targets[i].ProjectID < targets[j].ProjectID
		}
		return targets[i].ID < targets[j].ID
	})
	return targets, self, nil
}

// runRestartAll performs the kill+restore round trip for each target in order.
//
// The sequence is deliberately serial: restore recreates the tmux session under a
// deterministic name, so the old one has to be reaped first, and each call also
// does git-worktree work. RestoreAll in the daemon is serial for the same reason.
func (c *commandContext) runRestartAll(ctx context.Context, cmd *cobra.Command, targets []sessionDTO, opts restartAllOptions) []restartOutcome {
	results := make([]restartOutcome, 0, len(targets))
	progress := cmd.ErrOrStderr()

	for i, sess := range targets {
		outcome := restartOutcome{SessionID: sess.ID, ProjectID: sess.ProjectID, Kind: sess.Kind}

		if !opts.json {
			_, _ = fmt.Fprintf(progress, "[%d/%d] %s ... ", i+1, len(targets), sess.ID)
		}

		// The exited-state filter in restartAllTargets only ran once, at
		// selection time. The confirmation prompt and every earlier target's
		// settle-delay + restore round trip can add up to a real delay before
		// this target's turn — long enough for it to legitimately finish and
		// exit in the meantime. Re-check its current state right before killing
		// it, closing most of that window (short of a fully atomic server-side
		// guard, which is out of scope for this CLI-only command).
		current, err := c.fetchSessionByID(ctx, sess.ID)
		if err != nil {
			outcome.Status = restartStatusFailed
			outcome.Detail = "recheck: " + err.Error()
			results = append(results, outcome)
			if !opts.json {
				_, _ = fmt.Fprintln(progress, "failed (recheck)")
			}
			continue
		}
		if current.Activity.State == string(domain.ActivityExited) {
			outcome.Status = restartStatusSkippedExited
			outcome.Detail = "exited before this session's turn in the restart loop"
			results = append(results, outcome)
			if !opts.json {
				_, _ = fmt.Fprintln(progress, "skipped (exited)")
			}
			continue
		}

		preserved, err := c.restartKill(ctx, sess.ID)
		if err != nil {
			outcome.Status = restartStatusFailed
			outcome.Detail = "kill: " + err.Error()
			results = append(results, outcome)
			if !opts.json {
				_, _ = fmt.Fprintln(progress, "failed (kill)")
			}
			continue
		}

		// Let the runtime finish reaping the tmux session before restore recreates
		// one under the same name.
		if opts.settleDelay > 0 {
			select {
			case <-time.After(opts.settleDelay):
			case <-ctx.Done():
				outcome.Status = restartStatusFailed
				outcome.Detail = "interrupted after kill; session left terminated"
				results = append(results, outcome)
				if !opts.json {
					_, _ = fmt.Fprintln(progress, "interrupted")
				}
				// The remaining targets were never reached — record that
				// explicitly instead of dropping them from the output, so a
				// caller can tell which sessions still need a manual restore.
				for _, remaining := range targets[i+1:] {
					results = append(results, restartOutcome{
						SessionID: remaining.ID,
						ProjectID: remaining.ProjectID,
						Kind:      remaining.Kind,
						Status:    restartStatusFailed,
						Detail:    "interrupted before this session was reached",
					})
				}
				return results
			}
		}

		if err := c.restartRestore(ctx, sess.ID); err != nil {
			outcome.Status = restartStatusFailed
			outcome.Detail = "restore: " + err.Error()
			results = append(results, outcome)
			if !opts.json {
				_, _ = fmt.Fprintln(progress, "failed (restore) — session left terminated")
			}
			continue
		}

		outcome.Status = restartStatusRestarted
		if preserved {
			outcome.Status = restartStatusPreserved
		}
		results = append(results, outcome)
		if !opts.json {
			_, _ = fmt.Fprintln(progress, "ok")
		}
	}
	return results
}

// fetchSessionByID re-fetches a single session's current record, used to
// re-check its activity state right before killing it (see runRestartAll).
func (c *commandContext) fetchSessionByID(ctx context.Context, id string) (sessionDTO, error) {
	var res sessionResponse
	if err := c.getJSON(ctx, "sessions/"+url.PathEscape(id), &res); err != nil {
		return sessionDTO{}, err
	}
	return res.Session, nil
}

func (c *commandContext) restartKill(ctx context.Context, id string) (preserved bool, err error) {
	var res killSessionResponse
	if err := c.postJSON(ctx, "sessions/"+url.PathEscape(id)+"/kill", struct{}{}, &res); err != nil {
		return false, err
	}
	return !res.Freed, nil
}

func (c *commandContext) restartRestore(ctx context.Context, id string) error {
	var res restoreSessionResponse
	return c.postJSON(ctx, "sessions/"+url.PathEscape(id)+"/restore", struct{}{}, &res)
}

func writeRestartPlan(cmd *cobra.Command, targets []sessionDTO, asJSON bool) error {
	if asJSON {
		out := restartAllOutput{Data: make([]restartOutcome, 0, len(targets))}
		for _, sess := range targets {
			out.Data = append(out.Data, restartOutcome{
				SessionID: sess.ID,
				ProjectID: sess.ProjectID,
				Kind:      sess.Kind,
				Status:    restartStatusPlanned,
			})
		}
		out.Meta.Total = len(targets)
		return writeJSON(cmd.OutOrStdout(), out)
	}

	w := cmd.OutOrStdout()
	if _, err := fmt.Fprintf(w, "would restart %d session%s:\n", len(targets), pluralS(len(targets))); err != nil {
		return err
	}
	for _, sess := range targets {
		if _, err := fmt.Fprintf(w, "  %s  (%s, %s)\n", sess.ID, sess.ProjectID, sess.Kind); err != nil {
			return err
		}
	}
	return nil
}

func writeRestartResults(cmd *cobra.Command, results []restartOutcome, asJSON bool) error {
	var restarted, skipped, failed int
	for _, r := range results {
		switch r.Status {
		case restartStatusRestarted, restartStatusPreserved:
			restarted++
		case restartStatusSkippedExited:
			skipped++
		case restartStatusFailed:
			failed++
		}
	}

	if asJSON {
		out := restartAllOutput{Data: results}
		if out.Data == nil {
			out.Data = []restartOutcome{}
		}
		out.Meta.Total = len(results)
		out.Meta.Restarted = restarted
		out.Meta.Skipped = skipped
		out.Meta.Failed = failed
		if err := writeJSON(cmd.OutOrStdout(), out); err != nil {
			return err
		}
		if failed > 0 {
			return fmt.Errorf("%d of %d sessions failed to restart", failed, len(results))
		}
		return nil
	}

	w := cmd.OutOrStdout()
	if _, err := fmt.Fprintf(w, "restarted %d of %d session%s\n", restarted, len(results), pluralS(len(results))); err != nil {
		return err
	}
	if skipped > 0 {
		if _, err := fmt.Fprintf(w, "skipped %d session%s (exited before their turn in the restart loop)\n", skipped, pluralS(skipped)); err != nil {
			return err
		}
	}
	if failed == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w, "failures:"); err != nil {
		return err
	}
	for _, r := range results {
		if r.Status != restartStatusFailed {
			continue
		}
		if _, err := fmt.Fprintf(w, "  %s: %s\n", r.SessionID, r.Detail); err != nil {
			return err
		}
	}
	return fmt.Errorf("%d of %d sessions failed to restart", failed, len(results))
}

func confirmRestart(cmd *cobra.Command, targets []sessionDTO) (bool, error) {
	w := cmd.OutOrStdout()
	if _, err := fmt.Fprintf(w, "about to kill and restore %d session%s:\n", len(targets), pluralS(len(targets))); err != nil {
		return false, err
	}
	for _, sess := range targets {
		if _, err := fmt.Fprintf(w, "  %s  (%s, %s)\n", sess.ID, sess.ProjectID, sess.Kind); err != nil {
			return false, err
		}
	}
	if _, err := fmt.Fprint(w, "continue? [y/N] "); err != nil {
		return false, err
	}

	// A bare newline or a closed stdin is a decline, not a failure: the prompt
	// defaults to "no", so treat an empty read as such and report anything else.
	// Same reader-based pattern as confirmSessionCleanup/confirmProjectRemoval.
	reader := bufio.NewReader(cmd.InOrStdin())
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}
