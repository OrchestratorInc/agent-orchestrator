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

const (
	SpawnStepModelCheck  = "model_check"
	SpawnStepBranchFetch = "branch_fetch"
	SpawnStepWorktree    = "worktree"
	SpawnStepSetup       = "setup"
	SpawnStepAgentStart  = "agent_start"
)

type SpawnTrace struct {
	mu         sync.Mutex
	steps      map[string]int64
	failedStep string
	model      string
	mode       domain.SessionMode
	async      bool
}

type spawnTraceKey struct{}

func WithSpawnTrace(ctx context.Context) (context.Context, *SpawnTrace) {
	trace := &SpawnTrace{steps: map[string]int64{}}
	return context.WithValue(ctx, spawnTraceKey{}, trace), trace
}

func spawnTraceFrom(ctx context.Context) *SpawnTrace {
	trace, _ := ctx.Value(spawnTraceKey{}).(*SpawnTrace)
	return trace
}

func carrySpawnTrace(from, to context.Context) context.Context {
	if trace := spawnTraceFrom(from); trace != nil {
		return context.WithValue(to, spawnTraceKey{}, trace)
	}
	return to
}

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

const customSpawnModel = "custom"

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

type SpawnTraceFacts struct {
	StepMs     map[string]int64
	FailedStep string
	Model      string
	Mode       domain.SessionMode
	Async      bool
}

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

func SpawnTraceFrom(ctx context.Context) *SpawnTrace { return spawnTraceFrom(ctx) }

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

func (e *ClassifiedSpawnError) SentryTags() map[string]string {
	return map[string]string{"failed_step": e.Step, "cause": e.Cause}
}

func ClassifySpawnError(ctx context.Context, err error) error { return classifySpawnError(ctx, err) }

func classifySpawnError(ctx context.Context, err error) error {
	var already *ClassifiedSpawnError
	if err == nil || errors.As(err, &already) {
		return err
	}
	step, cause := ClassifySpawnFailure(err, spawnTraceFrom(ctx).Facts())
	return &ClassifiedSpawnError{Err: err, Step: step, Cause: cause}
}
