package sessionmanager

import (
	"context"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
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

// beginSpawnStep times one step. The returned func records its duration and,
// on the first failing step, which step failed. Safe without a trace.
func beginSpawnStep(ctx context.Context, step string) func(error) {
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

func noteSpawnLaunch(ctx context.Context, mode domain.SessionMode, model string, async bool) {
	trace := spawnTraceFrom(ctx)
	if trace == nil {
		return
	}
	trace.mu.Lock()
	defer trace.mu.Unlock()
	if mode != "" {
		trace.mode = mode
	}
	if model != "" {
		trace.model = model
	}
	trace.async = trace.async || async
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
