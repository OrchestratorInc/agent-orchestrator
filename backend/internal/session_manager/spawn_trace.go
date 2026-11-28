package sessionmanager

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

// Spawn steps reported by spawn telemetry. The vocabulary is closed: it names
// where a start spent its time and where it failed, never what it worked on.
const (
	SpawnStepModelCheck  = "model_check"
	SpawnStepBranchFetch = "branch_fetch"
	SpawnStepWorktree    = "worktree"
	SpawnStepSetup       = "setup"
	SpawnStepAgentStart  = "agent_start"
)

// SpawnTrace collects one start's step timings and the facts its outcome event
// reports. The session service attaches it to the spawn context; an
// asynchronous Chat start carries it into the background, so the trace is
// written from two goroutines and guards itself.
type SpawnTrace struct {
	mu         sync.Mutex
	steps      map[string]int64
	failedStep string
	model      string
	mode       domain.SessionMode
	async      bool
}

type spawnTraceKey struct{}

// WithSpawnTrace returns ctx carrying a new trace.
func WithSpawnTrace(ctx context.Context) (context.Context, *SpawnTrace) {
	trace := &SpawnTrace{steps: map[string]int64{}}
	return context.WithValue(ctx, spawnTraceKey{}, trace), trace
}

func spawnTraceFrom(ctx context.Context) *SpawnTrace {
	trace, _ := ctx.Value(spawnTraceKey{}).(*SpawnTrace)
	return trace
}

// carrySpawnTrace copies from's trace onto to (a background context).
func carrySpawnTrace(from, to context.Context) context.Context {
	if trace := spawnTraceFrom(from); trace != nil {
		return context.WithValue(to, spawnTraceKey{}, trace)
	}
	return to
}

// BeginSpawnStep times one step. The returned func records its duration and,
// on the first failing step, which step failed. Safe without a trace.
func BeginSpawnStep(ctx context.Context, step string) func(error) {
	trace := spawnTraceFrom(ctx)
	if trace == nil {
		return func(error) {}
	}
	started := time.Now()
	return func(err error) {
		trace.mu.Lock()
		defer trace.mu.Unlock()
		trace.steps[step] += time.Since(started).Milliseconds()
		if err != nil && trace.failedStep == "" {
			trace.failedStep = step
		}
	}
}

// NoteSpawnLaunch records the launch's mode and whether it went on in the
// background. An empty mode is ignored and async is sticky-true.
func NoteSpawnLaunch(ctx context.Context, mode domain.SessionMode, async bool) {
	trace := spawnTraceFrom(ctx)
	if trace == nil {
		return
	}
	trace.mu.Lock()
	defer trace.mu.Unlock()
	if mode != "" {
		trace.mode = mode
	}
	trace.async = trace.async || async
}

// customSpawnModel buckets all concrete model IDs, whether typed or discovered.
// Gateway and custom-provider catalogs can list private org-internal names.
const customSpawnModel = "custom"

// NoteSpawnModel reports only a fixed Claude family alias (optionally [1m])
// confirmed by resolution. Every concrete or unknown ID becomes "custom".
func NoteSpawnModel(ctx context.Context, model string, listed bool) {
	trace := spawnTraceFrom(ctx)
	if trace == nil || model == "" {
		return
	}
	model = strings.ToLower(strings.TrimSpace(model))
	if !listed || !containsString(claudeFamilyAliases, strings.TrimSuffix(model, "[1m]")) {
		model = customSpawnModel
	}
	trace.mu.Lock()
	defer trace.mu.Unlock()
	trace.model = model
}

// SpawnTraceFacts is a snapshot of a trace.
type SpawnTraceFacts struct {
	// StepMs holds only the steps that ran, keyed "<step>_ms".
	StepMs     map[string]int64
	FailedStep string
	Model      string
	Mode       domain.SessionMode
	Async      bool
}

// Facts snapshots the trace. A nil trace reports nothing.
func (t *SpawnTrace) Facts() SpawnTraceFacts {
	if t == nil {
		return SpawnTraceFacts{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	steps := make(map[string]int64, len(t.steps))
	for step, ms := range t.steps {
		steps[step+"_ms"] = ms
	}
	return SpawnTraceFacts{StepMs: steps, FailedStep: t.failedStep, Model: t.model, Mode: t.mode, Async: t.async}
}

// SpawnTraceFrom returns the trace ctx carries, or nil.
func SpawnTraceFrom(ctx context.Context) *SpawnTrace { return spawnTraceFrom(ctx) }

// ClassifySpawnFailure names the step a spawn failed in and why, from closed
// vocabularies only: failed_step is a SpawnStep* value, "prompt" or "other";
// cause is timeout, a worktree cause (workspaceCause), an agent cause
// (agentStartCause), binary_missing for an agent that is not installed, or
// other. Error text is read here and never leaves the daemon.
func ClassifySpawnFailure(err error, facts SpawnTraceFacts) (step, cause string) {
	step = facts.FailedStep
	if step == "" {
		switch {
		case errors.Is(err, ErrSpawnPrompt), errors.Is(err, ErrSpawnPromptDelivery), errors.Is(err, ErrSpawnDeliverPrompt):
			step = "prompt"
		case errors.Is(err, ErrWorkspaceCreate):
			step = SpawnStepWorktree
		case errors.Is(err, ErrWorkspaceProvision):
			step = SpawnStepSetup
		case errors.Is(err, ErrRuntimeCreate), errors.Is(err, ErrChatController):
			step = SpawnStepAgentStart
		case errors.Is(err, ErrUnsupportedModel), errors.Is(err, ports.ErrUnsupportedEffort), errors.Is(err, ports.ErrModelCapabilitiesUnavailable):
			step = SpawnStepModelCheck
		default:
			step = "other"
		}
	}
	var timedOut *SpawnTimeoutError
	if errors.As(err, &timedOut) || errors.Is(err, context.DeadlineExceeded) {
		return step, "timeout"
	}
	switch step {
	case SpawnStepBranchFetch, SpawnStepWorktree:
		return step, workspaceCause(err)
	case SpawnStepAgentStart:
		return step, agentStartCause(err)
	}
	if errors.Is(err, ports.ErrAgentBinaryNotFound) {
		return step, "binary_missing"
	}
	return step, "other"
}

// workspaceCause names why a worktree could not be created: a typed refusal
// AO raised itself, then git's own failure (the adapter's FailureCause, see
// gitworktree.classifyGitFailure), then the operating-system error behind a
// non-git step such as creating the managed folder.
func workspaceCause(err error) string {
	switch {
	case errors.Is(err, ports.ErrWorkspacePathOccupied):
		return "path_exists"
	case errors.Is(err, ports.ErrWorkspaceBranchCheckedOutElsewhere), errors.Is(err, ports.ErrWorkspaceBranchAlreadyExists):
		return "branch_exists"
	case errors.Is(err, ports.ErrWorkspaceLocked):
		return "locked"
	case errors.Is(err, ports.ErrWorkspaceNotARepo):
		return "not_a_repo"
	case errors.Is(err, ports.ErrWorkspaceDefaultBranchUnresolved):
		return "no_default_branch"
	case errors.Is(err, ports.ErrWorkspaceBranchNotFetched):
		return "ref_missing"
	case errors.Is(err, ports.ErrWorkspaceBranchInvalid):
		return "invalid_branch"
	case errors.Is(err, ErrProjectNotResolvable):
		return "repo_missing"
	}
	var git interface{ FailureCause() string }
	if errors.As(err, &git) {
		if cause := git.FailureCause(); cause != "other" {
			return cause
		}
	}
	if cause := osErrorCause(err); cause != "" {
		return cause
	}
	if errors.Is(err, exec.ErrNotFound) {
		return "git_missing"
	}
	return "other"
}

// osErrorCause names an operating-system failure shared by worktree and agent
// starts: disk_full, permission, path_too_long, or "" for none of them. Typed
// errors decide first, so a path in the message cannot override the real
// errno; message text is only the fallback for errors that lost their type.
func osErrorCause(err error) string {
	switch {
	case errors.Is(err, syscall.ENOSPC):
		return "disk_full"
	case errors.Is(err, os.ErrPermission):
		return "permission"
	case errors.Is(err, syscall.ENAMETOOLONG):
		return "path_too_long"
	}
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "no space left on device"), strings.Contains(text, "not enough space on the disk"),
		strings.Contains(text, "disk quota exceeded"):
		return "disk_full"
	case strings.Contains(text, "permission denied"), strings.Contains(text, "access is denied"),
		strings.Contains(text, "operation not permitted"), strings.Contains(text, "read-only file system"):
		return "permission"
	case strings.Contains(text, "file name too long"), strings.Contains(text, "filename too long"):
		return "path_too_long"
	}
	return ""
}

// agentStartCause names why a terminal agent or Chat controller did not start:
// not_logged_in, killed, binary_missing, disk_full, permission, path_too_long,
// a cause the chat adapter attached itself (agent_error: the agent answered
// the start with an error; host_failed: AO's chat host process did not come
// up), exited, no_response, incompatible or other.
func agentStartCause(err error) string {
	if errors.Is(err, ports.ErrChatAuthRequired) {
		return "not_logged_in"
	}
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "signal: killed"):
		return "killed"
	case strings.Contains(text, "not logged in"), strings.Contains(text, "please run /login"), strings.Contains(text, "authentication required"):
		return "not_logged_in"
	// Checked before the adapter's own cause: a binary resolved while AO's
	// chat host prepares the launch fails inside the host start.
	case errors.Is(err, ports.ErrAgentBinaryNotFound), errors.Is(err, exec.ErrNotFound),
		strings.Contains(text, "executable file not found"), strings.Contains(text, "not a valid win32 application"):
		return "binary_missing"
	}
	if cause := osErrorCause(err); cause != "" {
		return cause
	}
	var adapter interface{ FailureCause() string }
	if errors.As(err, &adapter) {
		switch cause := adapter.FailureCause(); {
		// An agent that refuses the ACP initialize handshake answered, but the
		// useful label is that it does not speak AO's protocol.
		case cause == "agent_error" && errors.Is(err, ports.ErrChatDriverIncompatible):
			return "incompatible"
		case cause == "agent_error" || cause == "host_failed":
			return cause
		}
	}
	switch {
	case errors.Is(err, ports.ErrChatHostNotRunning), strings.Contains(text, "peer disconnected"),
		strings.Contains(text, "exit status"), strings.Contains(text, "exited"),
		strings.Contains(text, "broken pipe"), strings.Contains(text, "closed pipe"):
		return "exited"
	case strings.Contains(text, "timed out"), strings.Contains(text, "timeout"), strings.Contains(text, "no response"), strings.Contains(text, "did not respond"):
		return "no_response"
	case errors.Is(err, ports.ErrChatDriverIncompatible):
		return "incompatible"
	}
	return "other"
}

// ClassifiedSpawnError carries a spawn failure's step and cause to Sentry as
// tags while reading exactly like the error it wraps.
type ClassifiedSpawnError struct {
	Err         error
	Step, Cause string
}

func (e *ClassifiedSpawnError) Error() string {
	if e.Err == nil {
		return "spawn failed"
	}
	return e.Err.Error()
}

func (e *ClassifiedSpawnError) Unwrap() error { return e.Err }

// SentryTags implements the sentryobs tagged-error hook.
func (e *ClassifiedSpawnError) SentryTags() map[string]string {
	return map[string]string{"failed_step": e.Step, "cause": e.Cause}
}

// ClassifySpawnError wraps err with its step and cause from ctx's trace.
func ClassifySpawnError(ctx context.Context, err error) error { return classifySpawnError(ctx, err) }

// classifySpawnError wraps err with its step and cause from ctx's trace.
func classifySpawnError(ctx context.Context, err error) error {
	var already *ClassifiedSpawnError
	if err == nil || errors.As(err, &already) {
		return err
	}
	step, cause := ClassifySpawnFailure(err, spawnTraceFrom(ctx).Facts())
	return &ClassifiedSpawnError{Err: err, Step: step, Cause: cause}
}
