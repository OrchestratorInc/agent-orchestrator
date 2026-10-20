package coder

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/activity"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

// Worker environment for a bring-your-own connection's working directory and
// secret-file protection (mirrored by the worker package's constants of the
// same names).
const (
	WorkingDirectoryEnv   = "AO_WORKER_WORKING_DIRECTORY"
	ProtectSecretFilesEnv = "AO_WORKER_PROTECT_SECRET_FILES"
	// workspaceNameEnv names the session's Coder workspace for the project's
	// scripts and the agent; a worker launched by AO does not otherwise see it.
	workspaceNameEnv = "CODER_WORKSPACE_NAME"
	// cleanupHookFile is the hook the worker installs in its data directory
	// (worker.CleanupHookFile) when the project has Cleanup commands.
	cleanupHookFile = "cleanup-hook.sh"
)

const (
	// StageCleanup is the project's Cleanup commands, run before AO deletes
	// the session's workspace.
	StageCleanup  = "project_cleanup"
	cleanupMarker = "__AO_PROJECT_CLEANUP__"
	// cleanupSkipped is what the hook command reports when the worker never
	// installed a hook (no Cleanup commands, or the worker never started).
	cleanupSkipped = "skipped"
	// cleanupBudget bounds the whole Cleanup run, so the workspace is still
	// deleted well within the reconciler's deletion deadline. Each command
	// also has its own 10-minute limit inside the hook.
	cleanupBudget = 10 * time.Minute
	// cleanupMargin is extra time AO waits beyond cleanupBudget for the
	// terminal to open and the result to arrive.
	cleanupMargin = 20 * time.Second
	// cleanupRecordTTL is how long a finished run is remembered, so a Delete
	// the reconciler re-issues while the workspace winds down never runs the
	// commands a second time.
	cleanupRecordTTL = time.Hour
	// cleanupInlineWait is how long one Delete waits for a Cleanup run before
	// handing the workspace back to the reconciler. Most Cleanup commands
	// finish within it, so the workspace is deleted in the same pass and a
	// later tick is only needed for slow ones.
	cleanupInlineWait = 5 * time.Second
	// cleanupStaleAfter is added to a run's budget and margin before a run that
	// never reported is treated as finished, so a lost or wedged run can never
	// hold a deletion indefinitely.
	cleanupStaleAfter = 30 * time.Second
)

// cleanupRun is one workspace build's Cleanup run. done closes when the run
// finishes; the run itself never fails a Delete.
type cleanupRun struct {
	done    chan struct{}
	started time.Time
	// limit is how long the run may take before Delete stops waiting for it.
	limit time.Duration
}

// cleanupRuns tracks Cleanup per workspace build in this control-plane
// process. The reconciler finishes a whole pass before starting the next, so
// Delete starts the run in the background and reports it unfinished instead
// of holding every other sandbox in the pass for up to cleanupBudget. The hook
// itself runs the commands at most once per workspace, so a run another
// replica starts only waits for and reports the first one.
var cleanupRuns sync.Map // workspace ID + "/" + build ID -> *cleanupRun

// addLifecycleEnvironment adds the workspace name and the connection's working
// directory to a workspace-user worker's environment.
func (c *Client) addLifecycleEnvironment(environment map[string]string, workspaceName string) {
	if workspaceName != "" {
		environment[workspaceNameEnv] = workspaceName
	}
	if c.workingDirectory != "" {
		environment[WorkingDirectoryEnv] = c.workingDirectory
	}
}

// cleanupFinished reports whether Delete may remove the workspace. The first
// Delete of a running, reachable workspace starts the project's Cleanup
// commands (the hook the worker installed) through the agent PTY in the
// background and waits up to cleanupInlineWait for it; while the run is still
// going Delete returns false and the reconciler re-issues it on a later tick.
// A workspace that is not running or reachable, or a
// client without a durable root, has nothing to clean up. Cleanup never
// blocks deletion: a failure or a run over cleanupBudget is only reported as
// a stage. Like local cleanup it runs only before permanent removal, never on
// stop or pause.
func (c *Client) cleanupFinished(ctx context.Context, id sandbox.ID) bool {
	if c.durableRoot == "" || sandbox.CleanupSkipped(ctx) {
		return true
	}
	var view workspace
	if err := c.do(ctx, http.MethodGet, "/api/v2/workspaces/"+url.PathEscape(string(id)), nil, &view); err != nil {
		return true
	}
	key := string(id) + "/" + view.LatestBuild.ID
	if existing, ok := cleanupRuns.Load(key); ok {
		return c.awaitCleanup(ctx, id, existing.(*cleanupRun))
	}
	// The project's current Cleanup, when the caller read it: an empty list
	// means the project has no Cleanup now, so nothing runs.
	current, known := sandbox.CleanupCommands(ctx)
	if known && len(current) == 0 {
		return true
	}
	agent, ok := c.selectAgent(view)
	if normalizeState(view.LatestBuild.Status) != sandbox.StateRunning || !ok || !agentReady(agent) {
		return true
	}
	ptyURL, err := url.Parse(c.baseURL + "/api/v2/workspaceagents/" + url.PathEscape(agent.ID) + "/pty")
	if err != nil {
		return true
	}
	override := ""
	if known {
		if payload, fits := cleanupCommandsPayload(current); fits {
			override = payload
		} else {
			slog.WarnContext(ctx, "coder: the project's current Cleanup is too large to pass to the workspace; running the commands from the session's start",
				"workspace_id", string(id), "limit_bytes", cleanupPayloadLimit)
		}
	}
	wait := c.cleanupWait
	if wait <= 0 {
		wait = cleanupBudget
	}
	run := &cleanupRun{done: make(chan struct{}), started: time.Now(), limit: wait + cleanupMargin}
	if existing, loaded := cleanupRuns.LoadOrStore(key, run); loaded {
		return c.awaitCleanup(ctx, id, existing.(*cleanupRun))
	}
	pruneCleanupRuns()
	// Detached from the reconcile pass's cancellation, but keeping its stage
	// observer so the outcome is still logged against the session.
	runCtx := context.WithoutCancel(ctx)
	attrs := c.stageAttrs(string(id), agent.ID)
	go func() {
		defer close(run.done)
		c.runCleanup(runCtx, ptyURL, wait, override, attrs)
	}()
	slog.InfoContext(ctx, "coder: running project cleanup before deleting the workspace",
		"workspace_id", string(id), "budget", wait)
	return c.awaitCleanup(ctx, id, run)
}

// awaitCleanup waits up to cleanupInlineWait for a Cleanup run and reports
// whether Delete may proceed. A run past its own limit plus cleanupStaleAfter
// counts as finished, so Cleanup can delay a deletion but never block it.
func (c *Client) awaitCleanup(ctx context.Context, id sandbox.ID, run *cleanupRun) bool {
	inline := c.cleanupInline
	if inline <= 0 {
		inline = cleanupInlineWait
	}
	timer := time.NewTimer(inline)
	defer timer.Stop()
	select {
	case <-run.done:
		return true
	case <-ctx.Done():
	case <-timer.C:
	}
	if time.Since(run.started) > run.limit+cleanupStaleAfter {
		slog.WarnContext(ctx, "coder: project cleanup never reported; deleting the workspace anyway",
			"workspace_id", string(id), "started", run.started)
		return true
	}
	return false
}

// runCleanup runs the worker-installed Cleanup hook through the agent PTY and
// reports the outcome as StageCleanup.
func (c *Client) runCleanup(ctx context.Context, ptyURL *url.URL, wait time.Duration, override string, attrs map[string]string) {
	finish := sandbox.BeginStage(ctx, StageCleanup)
	runCtx, cancel := context.WithTimeout(ctx, wait+cleanupMargin)
	defer cancel()
	attemptURL := *ptyURL
	query := attemptURL.Query()
	query.Set("width", "200")
	query.Set("height", "50")
	query.Set("command", "sh -c "+shellQuote(cleanupHookCommand(c.durableRoot, wait, override)))
	query.Set("backend_type", "buffered")
	attemptURL.RawQuery = query.Encode()
	// Opening the agent's terminal now and then ends at once with no output.
	// A quick failure like that is retried a few times; the hook's run-once
	// lock keeps a retry from running the commands twice.
	var result readyProbeResult
	var status string
	var err error
	for attempt := 1; ; attempt++ {
		started := time.Now()
		result, status, err = c.runMarkedCommandOnce(runCtx, &attemptURL, cleanupMarker, wait+cleanupMargin)
		if err == nil || !errors.Is(err, errTerminalNotReady) || attempt == cleanupTerminalAttempts ||
			time.Since(started) > cleanupQuickFailure || runCtx.Err() != nil {
			break
		}
		select {
		case <-runCtx.Done():
		case <-time.After(cleanupTerminalRetryDelay):
		}
	}
	switch {
	case err != nil:
		finish(fmt.Errorf("coder: project cleanup did not report: %w", err), attrs)
	case status == cleanupSkipped:
		attrs["result"] = cleanupSkipped
		finish(nil, attrs)
	case result.exitCode != 0:
		attrs["output"] = activity.Excerpt(result.output, 512)
		finish(fmt.Errorf("coder: project cleanup exited %d", result.exitCode), attrs)
	default:
		finish(nil, attrs)
	}
}

// A Cleanup whose terminal fails within cleanupQuickFailure is retried, up to
// cleanupTerminalAttempts in all, cleanupTerminalRetryDelay apart.
const (
	cleanupTerminalAttempts   = 3
	cleanupQuickFailure       = 5 * time.Second
	cleanupTerminalRetryDelay = 2 * time.Second
)

func pruneCleanupRuns() {
	cleanupRuns.Range(func(key, value any) bool {
		run := value.(*cleanupRun)
		select {
		case <-run.done:
			if time.Since(run.started) > cleanupRecordTTL {
				cleanupRuns.Delete(key)
			}
		default:
		}
		return true
	})
}

// cleanupPayloadLimit bounds the encoded Cleanup commands passed in the PTY
// command, which travels in the request URL; larger Cleanup falls back to the
// commands the worker installed.
const cleanupPayloadLimit = 12 << 10

// cleanupCommandsPayload is the gzip-compressed, base64-encoded Cleanup
// commands script for the project's current Cleanup, and whether it fits
// cleanupPayloadLimit.
func cleanupCommandsPayload(commands []string) (string, bool) {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write([]byte(worker.CleanupCommandsScript(commands))); err != nil {
		return "", false
	}
	if err := writer.Close(); err != nil {
		return "", false
	}
	payload := base64.StdEncoding.EncodeToString(compressed.Bytes())
	return payload, len(payload) <= cleanupPayloadLimit
}

// cleanupHookCommand runs the worker-installed Cleanup hook as the user that
// owns it (the worker's user: the workspace user itself, or the dedicated
// ao-worker account through sudo), under timeout(1) when the image has it,
// then prints the tail of its output and its exit status (or cleanupSkipped)
// after cleanupMarker. With override (a cleanupCommandsPayload), it first
// replaces the commands script beside the hook with the project's current
// Cleanup, as that user, so the hook keeps its working directory, session
// environment and run-once lock; if that fails the installed commands run.
func cleanupHookCommand(durableRoot string, budget time.Duration, override string) string {
	hook := shellQuote(path.Join(durableRoot, ".ao", "worker", cleanupHookFile))
	if sandbox.NormalizeCoderDurableRoot(durableRoot) == sandbox.CoderHomeDurableRoot {
		hook = `"$HOME"/.ao/worker/` + cleanupHookFile
	}
	seconds := strconv.Itoa(max(1, int(budget/time.Second)))
	command := "ao_hook=" + hook + "\n" +
		"ao_skip() { printf '\\n%s:%s\\n' " + cleanupMarker + " " + cleanupSkipped + "; exit 0; }\n" +
		"ao_owner=\n" +
		// The PTY runs as the workspace login user. A hook it owns (workspace
		// worker user) runs directly; any other hook runs only as its owner
		// through sudo, never as root or as the login user, so an ao-worker
		// agent that rewrites or chmods its hook cannot borrow the login
		// user's (sudo-capable) account.
		"if [ -O \"$ao_hook\" ] && [ -x \"$ao_hook\" ]; then set -- \"$ao_hook\"\n" +
		"elif sudo -n test -x \"$ao_hook\" 2>/dev/null; then\n" +
		"  ao_owner=$(sudo -n stat -c %U \"$ao_hook\" 2>/dev/null) || ao_skip\n" +
		"  case \"$ao_owner\" in ''|root|\"$(id -un)\") ao_skip ;; esac\n" +
		"  set -- sudo -n -H -u \"$ao_owner\" \"$ao_hook\"\n" +
		"else ao_skip; fi\n"
	if override != "" {
		write := shellQuote(`cat >"$1.tmp" && chmod 600 "$1.tmp" && mv "$1.tmp" "$1"`)
		command += "ao_commands=$(dirname \"$ao_hook\")/" + worker.CleanupCommandsFile + "\n" +
			"ao_payload=" + shellQuote(override) + "\n" +
			"if [ -n \"$ao_owner\" ]; then\n" +
			"  printf %s \"$ao_payload\" | base64 -d | gzip -dc | sudo -n -H -u \"$ao_owner\" sh -c " + write + " _ \"$ao_commands\"\n" +
			"else\n" +
			"  printf %s \"$ao_payload\" | base64 -d | gzip -dc | sh -c " + write + " _ \"$ao_commands\"\n" +
			"fi || echo \"AO: could not install the project's current Cleanup; running the commands from the session's start\" >&2\n"
	}
	return command +
		"if command -v timeout >/dev/null 2>&1; then\n" +
		"  ao_out=$(timeout -k 5 " + seconds + " \"$@\" 2>&1 </dev/null)\n" +
		"else\n" +
		"  ao_out=$(\"$@\" 2>&1 </dev/null)\n" +
		"fi\n" +
		"ao_code=$?\n" +
		"printf '%s\\n' \"$ao_out\" | tail -c " + strconv.Itoa(readyProbeOutputLimit) + "\n" +
		"printf '\\n%s:%s\\n' " + cleanupMarker + " \"$ao_code\"\n"
}
