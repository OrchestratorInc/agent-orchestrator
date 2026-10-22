package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	reviewSubmitRetryWindow   = 30 * time.Second
	reviewSubmitRetryInterval = 250 * time.Millisecond
)

// reviewRun mirrors the daemon's domain.ReviewRun for the CLI client.
type reviewRun struct {
	ID             string     `json:"id"`
	ReviewID       string     `json:"reviewId"`
	SessionID      string     `json:"sessionId"`
	BatchID        string     `json:"batchId"`
	Harness        string     `json:"harness"`
	PRURL          string     `json:"prUrl"`
	TargetSHA      string     `json:"targetSha"`
	Status         string     `json:"status"`
	Verdict        string     `json:"verdict"`
	Body           string     `json:"body"`
	GithubReviewID string     `json:"githubReviewId"`
	CreatedAt      time.Time  `json:"createdAt"`
	DeliveredAt    *time.Time `json:"deliveredAt,omitempty"`
}

type reviewState struct {
	PRURL       string     `json:"prUrl"`
	PRNumber    int        `json:"prNumber"`
	Title       string     `json:"title"`
	TargetSHA   string     `json:"targetSha"`
	Status      string     `json:"status"`
	LatestRun   *reviewRun `json:"latestRun,omitempty"`
	PreviousRun *reviewRun `json:"previousRun,omitempty"`
}

type listReviewsResponse struct {
	ReviewerHandleID string        `json:"reviewerHandleId"`
	Reviews          []reviewState `json:"reviews"`
}

// triggerReviewResponse mirrors controllers.TriggerReviewResponse: Created
// reports whether a new pass started, and the runs name which reviewer and
// commit it covers.
type triggerReviewResponse struct {
	Created           bool          `json:"created"`
	AutoInjectEnabled bool          `json:"autoInjectEnabled"`
	Runs              []reviewRun   `json:"runs"`
	Reviews           []reviewState `json:"reviews"`
}

// triggerReviewRequest mirrors controllers.TriggerReviewRequest.
type triggerReviewRequest struct {
	Harness            string             `json:"harness,omitempty"`
	AgentConfig        *reviewAgentConfig `json:"agentConfig,omitempty"`
	PRURL              string             `json:"prUrl,omitempty"`
	Source             string             `json:"source,omitempty"`
	RejectReviewedHead bool               `json:"rejectReviewedHead,omitempty"`
	Rerun              bool               `json:"rerun,omitempty"`
	EnableAutoInject   bool               `json:"enableAutoInject,omitempty"`
}

// reviewAgentConfig mirrors the model/effort subset of domain.AgentConfig a
// reviewer pass may override.
type reviewAgentConfig struct {
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
}

// reviewRunResponse mirrors controllers.ReviewRunResponse.
type reviewRunResponse struct {
	Review           reviewRun   `json:"review"`
	Reviews          []reviewRun `json:"reviews"`
	ReviewerHandleID string      `json:"reviewerHandleId"`
}

// submitReviewItem mirrors controllers.SubmitReviewItem.
type submitReviewItem struct {
	RunID          string `json:"runId"`
	Verdict        string `json:"verdict"`
	Body           string `json:"body,omitempty"`
	GithubReviewID string `json:"githubReviewId,omitempty"`
}

// submitReviewRequest mirrors controllers.SubmitReviewInput.
type submitReviewRequest struct {
	RunID          string             `json:"runId,omitempty"`
	Verdict        string             `json:"verdict,omitempty"`
	Body           string             `json:"body,omitempty"`
	GithubReviewID string             `json:"githubReviewId,omitempty"`
	Reviews        []submitReviewItem `json:"reviews,omitempty"`
}

type reviewSubmitOptions struct {
	session  string
	runID    string
	verdict  string
	body     string
	reviewID string
	reviews  string
}

type reviewSessionOptions struct {
	session string
}

type reviewTriggerOptions struct {
	session  string
	prURL    string
	harness  string
	model    string
	effort   string
	rerun    bool
	noInject bool
}

// envReviewSessionID is set only inside reviewer panes (see
// review.agentLauncher.runtimeEnv). Reviewers review; they never start reviews.
const envReviewSessionID = "AO_REVIEW_SESSION_ID"

// reviewTargetSession resolves the worker session a review command acts on:
// the explicit argument or --session, otherwise the calling AO session.
func reviewTargetSession(args []string, flag string) string {
	if len(args) == 1 {
		return strings.TrimSpace(args[0])
	}
	if session := strings.TrimSpace(flag); session != "" {
		return session
	}
	return strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
}

type reviewListOptions struct {
	json bool
}

func newReviewCommand(ctx *commandContext) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Manage AO code reviews of a worker's PR",
	}
	cmd.AddCommand(newReviewListCommand(ctx))
	cmd.AddCommand(newReviewSubmitCommand(ctx))
	cmd.AddCommand(newReviewCancelCommand(ctx))
	cmd.AddCommand(newReviewTriggerCommand(ctx))
	return cmd
}

func newReviewListCommand(ctx *commandContext) *cobra.Command {
	var opts reviewListOptions
	cmd := &cobra.Command{
		Use:     "ls [worker-session-id]",
		Aliases: []string{"list"},
		Short:   "List reviews for a worker session (default: this session)",
		Args:    atMostOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			session := reviewTargetSession(args, "")
			if session == "" {
				return usageError{errors.New("usage: worker session id is required outside an AO session")}
			}
			var res listReviewsResponse
			path := "sessions/" + url.PathEscape(session) + "/reviews"
			if err := ctx.getJSON(cmd.Context(), path, &res); err != nil {
				return err
			}
			if opts.json {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			return writeReviewList(cmd, session, res)
		},
	}
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output reviews as JSON")
	return cmd
}

func newReviewSubmitCommand(ctx *commandContext) *cobra.Command {
	var opts reviewSubmitOptions
	cmd := &cobra.Command{
		Use:   "submit [worker-session-id]",
		Short: "Record a reviewer's result for a worker's PR",
		Args:  atMostOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.submitReview(cmd, args, opts)
		},
	}
	// Reviewer agents routinely spell flags with underscores (--review_id) rather
	// than hyphens (--review-id); normalize so both resolve to the same flag.
	cmd.Flags().SetNormalizeFunc(func(_ *pflag.FlagSet, name string) pflag.NormalizedName {
		return pflag.NormalizedName(strings.ReplaceAll(name, "_", "-"))
	})
	cmd.Flags().StringVar(&opts.session, "session", "", "Worker session id (or pass it as the positional argument)")
	cmd.Flags().StringVar(&opts.runID, "run", "", "Review run id (required)")
	cmd.Flags().StringVar(&opts.verdict, "verdict", "", "Review verdict: approved or changes_requested (required)")
	cmd.Flags().StringVar(&opts.body, "body", "", "Review body: a path to a Markdown file, or - to read from stdin (so nothing is written into the worktree)")
	cmd.Flags().StringVar(&opts.reviewID, "review-id", "", "Id of the GitHub PR review just posted (the .id from the gh api POST that created the review)")
	cmd.Flags().StringVar(&opts.reviews, "reviews", "", "JSON review results array or object: a path, or - to read from stdin")
	return cmd
}

func (c *commandContext) submitReview(cmd *cobra.Command, args []string, opts reviewSubmitOptions) error {
	session := strings.TrimSpace(opts.session)
	if len(args) == 1 {
		session = strings.TrimSpace(args[0])
	}
	if session == "" {
		return usageError{errors.New("usage: worker session id is required (positional or --session)")}
	}
	if strings.TrimSpace(opts.reviews) != "" {
		return c.submitReviewBatch(cmd, session, opts)
	}
	runID := strings.TrimSpace(opts.runID)
	if runID == "" {
		return usageError{errors.New("usage: --run is required")}
	}
	verdict := strings.TrimSpace(opts.verdict)
	if verdict == "" {
		return usageError{errors.New("usage: --verdict is required (approved or changes_requested)")}
	}
	var body string
	if path := strings.TrimSpace(opts.body); path != "" {
		var raw []byte
		var err error
		if path == "-" {
			// Read the review from stdin so the reviewer never has to write a file
			// into its checkout (where it could be committed onto the worker branch).
			raw, err = io.ReadAll(cmd.InOrStdin())
		} else {
			raw, err = os.ReadFile(path)
		}
		if err != nil {
			return usageError{fmt.Errorf("read review body: %w", err)}
		}
		body = string(raw)
	}
	reviewID := strings.TrimSpace(opts.reviewID)
	path := "sessions/" + url.PathEscape(session) + "/reviews/submit"
	var res reviewRunResponse
	if err := c.postReviewJSON(cmd.Context(), path, submitReviewRequest{RunID: runID, Verdict: verdict, Body: body, GithubReviewID: reviewID}, &res); err != nil {
		return err
	}
	// A submit response always carries the recorded run's ID and verdict; a
	// structurally valid but half-populated result is a broken contract, not
	// a success to print.
	if strings.TrimSpace(res.Review.ID) == "" || strings.TrimSpace(res.Review.Verdict) == "" {
		return fmt.Errorf("daemon returned empty review result for %s", session)
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "recorded %s review for %s\n", res.Review.Verdict, session)
	return err
}

func (c *commandContext) submitReviewBatch(cmd *cobra.Command, session string, opts reviewSubmitOptions) error {
	if strings.TrimSpace(opts.runID) != "" || strings.TrimSpace(opts.verdict) != "" || strings.TrimSpace(opts.body) != "" || strings.TrimSpace(opts.reviewID) != "" {
		return usageError{errors.New("usage: --reviews cannot be combined with --run, --verdict, --body, or --review-id")}
	}
	reviews, err := readReviewItems(cmd, strings.TrimSpace(opts.reviews))
	if err != nil {
		return err
	}
	path := "sessions/" + url.PathEscape(session) + "/reviews/submit"
	var res reviewRunResponse
	if err := c.postReviewJSON(cmd.Context(), path, submitReviewRequest{Reviews: reviews}, &res); err != nil {
		return err
	}
	// Batch success is the recorded runs array: every returned entry must
	// carry its run ID and verdict. An empty array falls back to requiring
	// a fully-populated single review. Anything less is a broken contract,
	// not a success to print.
	if len(res.Reviews) > 0 {
		for _, run := range res.Reviews {
			if strings.TrimSpace(run.ID) == "" || strings.TrimSpace(run.Verdict) == "" {
				return fmt.Errorf("daemon returned empty review result for %s", session)
			}
		}
	} else if strings.TrimSpace(res.Review.ID) == "" || strings.TrimSpace(res.Review.Verdict) == "" {
		return fmt.Errorf("daemon returned empty review result for %s", session)
	}
	count := len(res.Reviews)
	if count == 0 {
		count = len(reviews)
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "recorded %d review(s) for %s\n", count, session)
	return err
}

// postReviewJSON retries only transport-level daemon unavailability. The
// service accepts an identical completed result idempotently, so replay is safe
// even when a connection drops after the daemon committed the first request.
// Validation/API errors still return immediately and are never retried.
func (c *commandContext) postReviewJSON(ctx context.Context, path string, body submitReviewRequest, out *reviewRunResponse) error {
	retryCtx, cancel := context.WithTimeout(ctx, reviewSubmitRetryWindow)
	defer cancel()

	var lastErr error
	for {
		err := c.postJSON(retryCtx, path, body, out)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !errors.Is(err, errDaemonUnavailable) {
			return err
		}
		lastErr = err
		if retryCtx.Err() != nil {
			return fmt.Errorf("%w; could not confirm the review result after retrying for %s. It may already be recorded; retry with the same review results", lastErr, reviewSubmitRetryWindow)
		}

		// Deps.Sleep keeps this loop deterministic in unit tests. The short
		// interval bounds cancellation latency without adding a retry goroutine.
		c.deps.Sleep(reviewSubmitRetryInterval)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if retryCtx.Err() != nil {
			return fmt.Errorf("%w; could not confirm the review result after retrying for %s. It may already be recorded; retry with the same review results", lastErr, reviewSubmitRetryWindow)
		}
	}
}

func newReviewCancelCommand(ctx *commandContext) *cobra.Command {
	var opts reviewSessionOptions
	cmd := &cobra.Command{
		Use:     "cancel [worker-session-id]",
		Aliases: []string{"stop"},
		Short:   "Cancel every running review for a worker's PR (default: this session)",
		Args:    atMostOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.stopReview(cmd, args, opts)
		},
	}
	cmd.Flags().StringVar(&opts.session, "session", "", "Worker session id (or pass it as the positional argument)")
	return cmd
}

func newReviewTriggerCommand(ctx *commandContext) *cobra.Command {
	var opts reviewTriggerOptions
	cmd := &cobra.Command{
		Use:     "trigger [worker-session-id]",
		Aliases: []string{"execute", "restart"},
		Short:   "Start an AO review of a worker's PR (default: this session's PR)",
		Long: `Start an independent AO reviewer on the worker session's open PR heads.

With no session argument the calling AO session is reviewed, so a worker can
request a review of its own PR. AO fetches the session's PRs fresh from the
provider first, so the pass reviews the commit really on the PR. Right after
opening a PR, pass --pr <url>: AO attaches it to the session if it does not
track it yet (never taking it from another active session) and reviews only it. The reviewer defaults to the session's reviewer
choice, then the project's reviewer config, then the project's default worker
agent and model; --agent, --model, and --effort override it for this pass only.

A head that is already being reviewed, or already has a review, is not reviewed
again: the command fails and says why. Pass --rerun to review the same commit
again, or to add a different --agent alongside one that is still running.

The worker session's review auto-inject is turned on so the reviewer's inline
review comments are delivered to the worker; pass --no-inject to leave
that setting unchanged. AO's review is internal: an approval is not a GitHub
approval and does not authorize merging.`,
		Args: atMostOneArg,
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.triggerReview(cmd, args, opts)
		},
	}
	f := cmd.Flags()
	f.SetNormalizeFunc(func(_ *pflag.FlagSet, name string) pflag.NormalizedName {
		if name == "agent" {
			name = "harness"
		}
		return pflag.NormalizedName(name)
	})
	f.StringVar(&opts.session, "session", "", "Worker session id (or pass it positionally; default: this AO session)")
	f.StringVar(&opts.prURL, "pr", "", "Review only this pull request URL, attaching it to the session if AO does not track it yet (default: every eligible PR on the session)")
	f.StringVar(&opts.harness, "harness", "", "Reviewer agent / --agent for this pass only (e.g. claude-code, codex)")
	f.StringVar(&opts.model, "model", "", "Reviewer model for this pass only")
	f.StringVar(&opts.effort, "effort", "", "Reviewer reasoning effort for this pass only")
	f.BoolVar(&opts.rerun, "rerun", false, "Review heads that already have a review again, or add another reviewer alongside a running one")
	f.BoolVar(&opts.noInject, "no-inject", false, "Leave the session's review auto-inject setting unchanged")
	return cmd
}

func (c *commandContext) triggerReview(cmd *cobra.Command, args []string, opts reviewTriggerOptions) error {
	if strings.TrimSpace(os.Getenv(envReviewSessionID)) != "" {
		return usageError{errors.New("ao review trigger cannot run inside a reviewer: reviewers review the requested PR and submit with `ao review submit`; they never start reviews")}
	}
	session := reviewTargetSession(args, opts.session)
	if session == "" {
		return usageError{errors.New("usage: worker session id is required (positional or --session) outside an AO session")}
	}
	req := triggerReviewRequest{
		Harness:            strings.TrimSpace(opts.harness),
		PRURL:              strings.TrimSpace(opts.prURL),
		RejectReviewedHead: !opts.rerun,
		Rerun:              opts.rerun,
		EnableAutoInject:   !opts.noInject,
	}
	if model, effort := strings.TrimSpace(opts.model), strings.TrimSpace(opts.effort); model != "" || effort != "" {
		req.AgentConfig = &reviewAgentConfig{Model: model, Effort: effort}
	}
	// A request from inside an AO session (a worker reviewing its own PR, or an
	// orchestrator) is labelled as such, so it is never mistaken for a person.
	if strings.TrimSpace(os.Getenv("AO_SESSION_ID")) != "" {
		req.Source = "agent"
	}
	path := "sessions/" + url.PathEscape(session) + "/reviews/trigger"
	var res triggerReviewResponse
	if err := c.postJSON(cmd.Context(), path, req, &res); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if !res.Created {
		// Only reachable from an older daemon that ignores the same-commit
		// policy; a current daemon answers 409 instead.
		_, err := fmt.Fprintf(out, "reused the existing review for %s\n", session)
		return err
	}
	if _, err := fmt.Fprintf(out, "started a new review for %s\n", session); err != nil {
		return err
	}
	for _, run := range startedRuns(res) {
		if _, err := fmt.Fprintf(out, "  %s reviewing %s at %s (run %s)\n", run.Harness, run.PRURL, shortReviewSHA(run.TargetSHA), run.ID); err != nil {
			return err
		}
	}
	switch {
	case res.AutoInjectEnabled:
		_, err := fmt.Fprintf(out, "turned on review auto-inject for %s; the reviewer's comments will be delivered to the session, and `ao review ls` shows the verdict\n", session)
		return err
	case opts.noInject:
		_, err := fmt.Fprintf(out, "left review auto-inject unchanged for %s; check results with `ao review ls %s`\n", session, session)
		return err
	}
	return nil
}

// startedRuns returns the running passes on the PR heads the trigger covered.
func startedRuns(res triggerReviewResponse) []reviewRun {
	var out []reviewRun
	for _, review := range res.Reviews {
		if review.LatestRun != nil && review.LatestRun.Status == "running" {
			out = append(out, *review.LatestRun)
		}
	}
	return out
}

func shortReviewSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

func (c *commandContext) stopReview(cmd *cobra.Command, args []string, opts reviewSessionOptions) error {
	session := reviewTargetSession(args, opts.session)
	if session == "" {
		return usageError{errors.New("usage: worker session id is required (positional or --session) outside an AO session")}
	}
	path := "sessions/" + url.PathEscape(session) + "/reviews/cancel"
	if err := c.postJSON(cmd.Context(), path, struct{}{}, nil); err != nil {
		return err
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "cancelled review for %s\n", session)
	return err
}

func writeReviewList(cmd *cobra.Command, session string, res listReviewsResponse) error {
	out := cmd.OutOrStdout()
	if len(res.Reviews) == 0 {
		_, err := fmt.Fprintf(out, "No reviews found for %s.\n", session)
		return err
	}

	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "PR\tSTATUS\tVERDICT\tTITLE"); err != nil {
		return err
	}
	for _, review := range res.Reviews {
		verdict := "-"
		if review.LatestRun != nil && review.LatestRun.Verdict != "" {
			verdict = review.LatestRun.Verdict
		}
		if _, err := fmt.Fprintf(tw, "#%d\t%s\t%s\t%s\n", review.PRNumber, review.Status, verdict, review.Title); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func readReviewItems(cmd *cobra.Command, path string) ([]submitReviewItem, error) {
	var raw []byte
	var err error
	if path == "-" {
		raw, err = io.ReadAll(cmd.InOrStdin())
	} else {
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, usageError{fmt.Errorf("read review results: %w", err)}
	}
	var req submitReviewRequest
	if err := json.Unmarshal(raw, &req); err == nil && len(req.Reviews) > 0 {
		return req.Reviews, nil
	}
	var reviews []submitReviewItem
	if err := json.Unmarshal(raw, &reviews); err != nil {
		return nil, usageError{fmt.Errorf("decode review results JSON: %w", err)}
	}
	if len(reviews) == 0 {
		return nil, usageError{errors.New("usage: --reviews requires at least one review result")}
	}
	return reviews, nil
}
