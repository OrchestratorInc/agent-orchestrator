package freestyle

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox/cleanuphook"
)

const (
	// inlineExecLimit is the longest exec-await Freestyle answers inline: a
	// longer timeoutMs is refused (400) unless the request is backgrounded.
	inlineExecLimit = 300 * time.Second
	// inlineCleanupBudget is the Cleanup budget when the exec must run
	// inline: with cleanuphook.Margin and the owner's processes' stop it
	// stays within inlineExecLimit. Coder's (and a backgrounded Freestyle
	// exec's) is the plan's budget.
	inlineCleanupBudget = inlineExecLimit - cleanuphook.Margin - 10*time.Second
	// backgroundHeader backgrounds a Freestyle API request: one still
	// running after that many seconds answers 202 with a request id to poll
	// (/v5/background-requests/{id}); the work goes on server side. A
	// backgrounded exec may run past inlineExecLimit.
	backgroundHeader       = "x-freestyle-background-after-secs"
	backgroundAfterSeconds = 20
	backgroundPoll         = 2 * time.Second

	// cleanupResumeWait bounds how long a Cleanup run waits for a paused or
	// stopped VM to run again before it deletes the VM without Cleanup. A
	// paused VM restores its memory in about a second.
	cleanupResumeWait = time.Minute
	cleanupResumePoll = 500 * time.Millisecond
)

// workerUser is the account the worker runs as in a Freestyle VM (the
// reconciler's default worker user, which the snapshot layout provides); the
// Cleanup hook runs as it.
const workerUser = "ao-worker"

// cleanupRuns tracks Cleanup per VM in this control-plane process.
var cleanupRuns cleanuphook.Tracker

// cleanupFinished reports whether Delete may remove the VM. The first Delete
// of a VM starts the project's Cleanup commands (the hook the worker
// installed) through an exec in the background and waits up to
// cleanuphook.InlineWait for it; while the run is still going Delete returns
// false and the reconciler re-issues it on a later tick. Like Coder's, the
// run is skipped for a project with no Cleanup now, a VM that cannot be read,
// and a Delete marked sandbox.WithoutCleanup, and never blocks deletion: a
// failure or a run over its budget is only reported. It runs only before
// permanent removal, never on pause or on a Recreate.
//
// Unlike a stopped Coder workspace, whose compute is gone, a paused VM keeps
// its memory and disk and resumes in about a second, so a paused (or
// stopped) VM is resumed for its Cleanup; one that does not resume is
// deleted with its Cleanup reported as skipped. A VM of an organization over
// its plan (sandbox.WithoutCleanupResume) is not resumed: its Cleanup is
// reported as skipped with the reason.
//
// Freestyle answers an exec-await inline for at most five minutes, so the
// hook runs as a backgrounded exec, with the same budget as Coder's (the
// plan's: the sum of its steps' time limits, up to two hours), polled until it
// ends. On a VM whose agent cannot run one, the hook runs
// inline with a budget of inlineCleanupBudget (4.5 minutes) instead.
func (c *Client) cleanupFinished(ctx context.Context, id sandbox.ID) bool {
	if sandbox.CleanupSkipped(ctx) {
		return true
	}
	key := string(id)
	if run, ok := cleanupRuns.Lookup(key); ok {
		return run.Await(ctx, c.cleanupInline, "provider_id", key)
	}
	if current, known := sandbox.CleanupCommands(ctx); known && len(current) == 0 {
		return true
	}
	environment, err := c.Get(ctx, id)
	if err != nil {
		return true
	}
	resume := false
	switch environment.State {
	case sandbox.StateRunning:
	case sandbox.StatePaused, sandbox.StateStopped:
		resume = true
	default:
		// Starting or pausing: not a VM the hook can run in yet. A later
		// Delete runs its Cleanup once it is running or paused (or the
		// deletion deadline deletes it without).
		return false
	}
	plan, _ := cleanuphook.NewPlan(ctx, "provider_id", key)
	// Detached from the reconcile pass's cancellation, but keeping its stage
	// observer and session event recorder.
	runCtx := context.WithoutCancel(ctx)
	attrs := map[string]string{"provider_id": key}
	skip := cleanuphook.Skipper(runCtx, attrs, plan.Steps)
	if reason, refused := sandbox.CleanupResumeRefused(ctx); resume && refused {
		// The organization is over its plan: its paused VM stays paused, and
		// the Cleanup is reported as not run, once. Nothing runs, so the
		// report takes no slot and never waits behind the organization's
		// own runs.
		attrs["resumed"] = "false"
		cleanupRuns.SkipOnce(runCtx, key, attrs, plan.Steps, reason)
		return true
	}
	wait := c.cleanupWait
	if wait <= 0 {
		wait = plan.Budget
	}
	resumeWait := c.cleanupResumeWait
	if resumeWait <= 0 {
		resumeWait = cleanupResumeWait
	}
	limit := wait + cleanuphook.Margin
	if resume {
		limit += resumeWait
	}
	run := cleanupRuns.Start(ctx, key, sandbox.CleanupOrg(ctx), wait, limit, func() {
		c.log.InfoContext(ctx, "freestyle: running project cleanup before deleting the VM",
			"provider_id", key, "budget", wait, "resume", resume)
		c.runCleanup(runCtx, id, resume, resumeWait, wait, plan, attrs)
	}, skip)
	return run.Await(ctx, c.cleanupInline, "provider_id", key)
}

// runCleanup resumes the VM when asked, runs the worker-installed Cleanup hook
// as its owner in one exec, reports the outcome as cleanuphook.Stage, and
// appends the run's steps to the session's events.
func (c *Client) runCleanup(
	ctx context.Context,
	id sandbox.ID,
	resume bool,
	resumeWait, wait time.Duration,
	plan cleanuphook.Plan,
	attrs map[string]string,
) {
	finish := sandbox.BeginStage(ctx, cleanuphook.Stage)
	steps := plan.Steps
	steps.User = workerUser
	if resume {
		attrs["resumed"] = "true"
		if err := c.resumeForCleanup(ctx, id, resumeWait); err != nil {
			cleanuphook.ReportNotRun(ctx, finish, attrs, steps,
				"The sandbox was paused and did not resume, so this Cleanup step did not run before the sandbox was deleted.",
				fmt.Errorf("freestyle: resume the paused VM for project cleanup: %w", err))
			return
		}
	}
	steps.StartedAt = time.Now()
	runCtx, cancel := context.WithTimeout(ctx, wait+cleanuphook.Margin)
	defer cancel()
	command := func(budget time.Duration) string {
		return cleanuphook.Command(cleanuphook.HookWord(cleanuphook.WorkspaceHook), budget, plan.Override, cleanuphook.Options{
			Runner: cleanuphook.AsRoot, Stop: cleanuphook.StopOwnerProcesses, Owner: workerUser,
		})
	}
	// Past inlineExecLimit the exec is backgrounded, so it may take the
	// whole budget. A VM whose agent cannot run it so refuses the timeout
	// (400) before running anything; the hook then runs inline, within
	// inlineCleanupBudget.
	background := wait+cleanuphook.Margin > inlineExecLimit
	attrs["exec"] = "inline"
	if background {
		attrs["exec"] = "background"
	}
	printed, status, err := c.execCleanupHook(runCtx, id, command(wait), wait+cleanuphook.Margin, background)
	var refused *HTTPError
	if background && errors.As(err, &refused) && refused.StatusCode == http.StatusBadRequest {
		c.log.WarnContext(ctx, "freestyle: backgrounded cleanup exec refused; running it inline with a shorter budget",
			"provider_id", string(id), "budget", inlineCleanupBudget, "error", err)
		attrs["exec"] = "inline"
		attrs["budget_seconds"] = strconv.Itoa(int(inlineCleanupBudget / time.Second))
		printed, status, err = c.execCleanupHook(runCtx, id, command(inlineCleanupBudget), inlineCleanupBudget+cleanuphook.Margin, false)
	}
	cleanuphook.Report(ctx, finish, attrs, steps, printed, status, err, "freestyle")
}

// resumeForCleanup starts a paused or stopped VM and waits up to wait for it
// to run.
func (c *Client) resumeForCleanup(ctx context.Context, id sandbox.ID, wait time.Duration) error {
	if err := c.Start(ctx, id); err != nil {
		return err
	}
	deadline := time.Now().Add(wait)
	for {
		environment, err := c.Get(ctx, id)
		if err != nil {
			return err
		}
		if environment.State == sandbox.StateRunning {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("VM %s is still %s %s after its start", id, environment.State, wait)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(cleanupResumePoll):
		}
	}
}

// execCleanupHook runs the hook command as root in one exec (the command runs
// the hook as its owner) and returns what the hook printed and its status.
// The exec waits for the whole run, so it is bounded by ctx and timeout
// rather than the client's per-call timeout. Backgrounded, a run past
// backgroundAfterSeconds is polled for (execInBackground); inline, timeout
// must stay within inlineExecLimit.
func (c *Client) execCleanupHook(ctx context.Context, id sandbox.ID, command string, timeout time.Duration, background bool) (string, string, error) {
	request := execRequest{
		Command:   command,
		LinuxUser: "root",
		TimeoutMs: int(timeout / time.Millisecond),
	}
	var result execResult
	var err error
	if background {
		result, err = c.execInBackground(ctx, id, request)
	} else {
		long := *c.http
		long.Timeout = 0
		err = c.doWith(ctx, &long, http.MethodPost, "/v5/vms/"+url.PathEscape(string(id))+"/exec-await", request, &result)
	}
	if err != nil {
		return "", "", err
	}
	printed, status, ok := cleanuphook.Split(result.Stdout)
	if !ok {
		exit := "without a status"
		if result.StatusCode != nil {
			exit = fmt.Sprintf("with status %d", *result.StatusCode)
		}
		return printed, "", fmt.Errorf("the hook command ended %s before reporting: %s",
			exit, truncate(strings.TrimSpace(result.Stderr), 512))
	}
	return printed, status, nil
}

// execInBackground runs an exec-await as a backgrounded request: it answers
// inline when it ends within backgroundAfterSeconds, else with 202 and a
// request id, polled every backgroundPoll until the exec's own response
// arrives or ctx ends.
func (c *Client) execInBackground(ctx context.Context, id sandbox.ID, request execRequest) (execResult, error) {
	status, body, err := c.send(ctx, http.MethodPost, "/v5/vms/"+url.PathEscape(string(id))+"/exec-await", request,
		map[string]string{backgroundHeader: strconv.Itoa(backgroundAfterSeconds)})
	requestID := ""
	for {
		if err != nil {
			return execResult{}, err
		}
		switch {
		case status == http.StatusAccepted:
			var pending struct {
				RequestID string `json:"requestId"`
			}
			if json.Unmarshal(body, &pending) == nil && pending.RequestID != "" {
				requestID = pending.RequestID
			}
			if requestID == "" {
				return execResult{}, errors.New("freestyle: backgrounded exec answered 202 without a request id")
			}
			select {
			case <-ctx.Done():
				return execResult{}, ctx.Err()
			case <-time.After(cmp.Or(c.backgroundPoll, backgroundPoll)):
			}
			status, body, err = c.send(ctx, http.MethodGet, "/v5/background-requests/"+url.PathEscape(requestID), nil, nil)
		case status == http.StatusNotFound:
			return execResult{}, fmt.Errorf("freestyle: backgrounded exec %s: %w", requestID, sandbox.ErrNotFound)
		case status < 200 || status > 299:
			return execResult{}, &HTTPError{StatusCode: status, Body: truncate(strings.TrimSpace(string(body)), 512)}
		default:
			var result execResult
			if err := json.Unmarshal(body, &result); err != nil {
				return execResult{}, fmt.Errorf("freestyle: decode exec response: %w", err)
			}
			return result, nil
		}
	}
}

// send makes one API request with extra headers and returns its status and
// bounded body, whatever the status.
func (c *Client) send(ctx context.Context, method, path string, body any, headers map[string]string) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, nil, fmt.Errorf("freestyle: encode %s request: %w", path, err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return 0, nil, fmt.Errorf("freestyle: %s %s: %w", method, path, err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBody))
	if err != nil {
		return 0, nil, fmt.Errorf("freestyle: read %s response: %w", path, err)
	}
	return response.StatusCode, raw, nil
}
