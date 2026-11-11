package sessionmanager

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
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
// cause is timeout, a git cause (branch_exists, path_exists, locked,
// permission, path_too_long, not_a_repo, killed), an agent cause (exited,
// no_response, not_logged_in, killed) or other. Error text is read here and
// never leaves the daemon.
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
		var git interface{ FailureCause() string }
		if errors.As(err, &git) {
			return step, git.FailureCause()
		}
	case SpawnStepAgentStart:
		return step, agentStartCause(err)
	}
	return step, "other"
}

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
	case errors.Is(err, ports.ErrChatHostNotRunning), strings.Contains(text, "peer disconnected"),
		strings.Contains(text, "exit status"), strings.Contains(text, "exited"),
		strings.Contains(text, "broken pipe"), strings.Contains(text, "closed pipe"):
		return "exited"
	case strings.Contains(text, "timed out"), strings.Contains(text, "timeout"), strings.Contains(text, "no response"), strings.Contains(text, "did not respond"):
		return "no_response"
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
