package docker

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox/cleanuphook"
)

// cleanupRuns tracks Cleanup per container in this control-plane process.
var cleanupRuns cleanuphook.Tracker

// cleanupFinished reports whether Delete may remove the worker container. The
// first Delete of a running container starts the project's Cleanup commands
// (the hook the worker installed) through a docker exec in the background and
// waits up to cleanuphook.InlineWait for it; while the run is still going
// Delete returns false and the reconciler re-issues it on a later tick. As on
// Coder, nothing runs for a project with no Cleanup now, a container that is
// not running (a paused session's worker is stopped, and starting it again
// would replay its spent bootstrap ticket; its Cleanup is reported as not
// run, once), or a Delete marked
// sandbox.WithoutCleanup, and Cleanup never blocks deletion: a failure or a
// run over its budget is only reported. A restarting container is left for a
// later Delete. It runs only before permanent removal, never on stop, pause
// or a Recreate.
func (c *Client) cleanupFinished(ctx context.Context, view containerView) bool {
	if sandbox.CleanupSkipped(ctx) {
		return true
	}
	key := view.ID
	if run, ok := cleanupRuns.Lookup(key); ok {
		return run.Await(ctx, c.cleanupInline, "provider_id", key)
	}
	if current, known := sandbox.CleanupCommands(ctx); known && len(current) == 0 {
		return true
	}
	if view.State.Restarting {
		// Coming back: a later Delete runs its Cleanup (or the deletion
		// deadline removes it without).
		return false
	}
	plan, _ := cleanuphook.NewPlan(ctx, "provider_id", key)
	if !view.State.Running || view.State.Paused {
		// Nothing runs in it; the session still says its Cleanup did not run.
		cleanupRuns.SkipOnce(ctx, key, map[string]string{"provider_id": key}, plan.Steps, cleanuphook.NotRunningReason)
		return true
	}
	wait := c.cleanupWait
	if wait <= 0 {
		wait = plan.Budget
	}
	// Detached from the reconcile pass's cancellation, but keeping its stage
	// observer and session event recorder.
	runCtx := context.WithoutCancel(ctx)
	attrs := map[string]string{"provider_id": key}
	run := cleanupRuns.Start(ctx, key, sandbox.CleanupOrg(ctx), wait, wait+cleanuphook.Margin, func() {
		slog.InfoContext(ctx, "docker: running project cleanup before deleting the worker",
			"provider_id", key, "budget", wait)
		c.runCleanup(runCtx, sandbox.ID(key), wait, plan, attrs)
	}, cleanuphook.Skipper(runCtx, attrs, plan.Steps))
	return run.Await(ctx, c.cleanupInline, "provider_id", key)
}

// runCleanup runs the worker-installed Cleanup hook in the container as the
// container's user (the worker's), reports the outcome as cleanuphook.Stage,
// and appends the run's steps to the session's events.
func (c *Client) runCleanup(ctx context.Context, id sandbox.ID, wait time.Duration, plan cleanuphook.Plan, attrs map[string]string) {
	finish := sandbox.BeginStage(ctx, cleanuphook.Stage)
	steps := plan.Steps
	steps.StartedAt = time.Now()
	runCtx, cancel := context.WithTimeout(ctx, wait+cleanuphook.Margin)
	defer cancel()
	command := cleanuphook.Command(cleanuphook.HookWord(cleanuphook.WorkspaceHook), wait, plan.Override,
		cleanuphook.Options{Runner: cleanuphook.AsLoginUser, Stop: cleanuphook.StopOwnerProcesses})
	printed, status, err := c.execCleanupHook(runCtx, id, command)
	cleanuphook.Report(ctx, finish, attrs, steps, printed, status, err, "docker")
}

type execCreateRequest struct {
	AttachStdout bool     `json:"AttachStdout"`
	AttachStderr bool     `json:"AttachStderr"`
	Cmd          []string `json:"Cmd"`
}

type execStartRequest struct {
	Detach bool `json:"Detach"`
	Tty    bool `json:"Tty"`
}

// execCleanupHook runs the hook command with docker exec as the container's
// user and returns what the hook printed and its status. The exec streams
// until the command ends, so it is bounded by ctx rather than the client's
// per-call timeout.
func (c *Client) execCleanupHook(ctx context.Context, id sandbox.ID, command string) (string, string, error) {
	var created struct {
		ID string `json:"Id"`
	}
	if err := c.do(ctx, http.MethodPost, "/containers/"+pathID(id)+"/exec", execCreateRequest{
		AttachStdout: true, AttachStderr: true, Cmd: []string{"sh", "-c", command},
	}, &created); err != nil {
		return "", "", err
	}
	if strings.TrimSpace(created.ID) == "" {
		return "", "", errors.New("docker: exec create response carried no exec id")
	}
	body, err := json.Marshal(execStartRequest{})
	if err != nil {
		return "", "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/exec/"+url.PathEscape(created.ID)+"/start", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	request.Header.Set("Content-Type", "application/json")
	long := *c.http
	long.Timeout = 0
	response, err := long.Do(request)
	if err != nil {
		return "", "", fmt.Errorf("docker: start cleanup exec: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		snippet, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorBody))
		return "", "", &HTTPError{StatusCode: response.StatusCode, Message: truncate(strings.TrimSpace(string(snippet)), 512)}
	}
	output, err := demuxTail(response.Body, 2*cleanuphook.OutputLimit)
	printed, status, ok := cleanuphook.Split(output)
	if !ok {
		if err == nil {
			err = errors.New("the hook command ended before reporting")
		}
		return printed, "", fmt.Errorf("docker: cleanup exec: %w", err)
	}
	return printed, status, nil
}

// demuxTail reads a docker exec's multiplexed stdout and stderr stream (an
// 8-byte header carrying the stream and frame size before each frame) as one
// output, keeping at least its last keep bytes.
func demuxTail(stream io.Reader, keep int) (string, error) {
	var output []byte
	var header [8]byte
	for {
		if _, err := io.ReadFull(stream, header[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return string(output), nil
			}
			return string(output), err
		}
		size := int64(binary.BigEndian.Uint32(header[4:]))
		frame, err := io.ReadAll(io.LimitReader(stream, size))
		output = append(output, frame...)
		if len(output) > 2*keep {
			output = append([]byte(nil), output[len(output)-keep:]...)
		}
		if err != nil {
			return string(output), err
		}
		if int64(len(frame)) < size {
			return string(output), io.ErrUnexpectedEOF
		}
	}
}
