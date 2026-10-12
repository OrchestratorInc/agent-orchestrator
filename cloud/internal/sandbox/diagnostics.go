package sandbox

import (
	"context"
	"time"
)

// Bootstrap stage names reported through a StageObserver. They are stable:
// operators query them (cloud/docs/support-runbook.md).
const (
	StageWorkspaceCreate  = "workspace_create"
	StageWorkspaceStart   = "workspace_start"
	StageAgentWait        = "agent_wait"
	StageDurableRootProbe = "durable_root_probe"
	// StageReadyProbe is a bring-your-own Coder connection's workspace ready
	// check, run as the workspace user after the agent is ready and before the
	// worker is installed.
	StageReadyProbe    = "ready_probe"
	StageWorkerInstall = "worker_install"
)

// StageReport describes one finished provider stage. Attrs carries small,
// non-secret identifiers (workspace id, owner, template, agent lifecycle).
type StageReport struct {
	Stage    string
	Duration time.Duration
	Err      error
	Attrs    map[string]string
}

// StageObserver receives provider stage reports. It must not block.
type StageObserver func(context.Context, StageReport)

type stageObserverKey struct{}

// WithStageObserver returns a context whose provider calls report their
// stages to observer.
func WithStageObserver(ctx context.Context, observer StageObserver) context.Context {
	return context.WithValue(ctx, stageObserverKey{}, observer)
}

// BeginStage starts timing a stage. The returned function reports it to the
// context's observer, if any, with the stage's outcome and attributes. It is
// safe to call when no observer is installed.
func BeginStage(ctx context.Context, stage string) func(err error, attrs map[string]string) {
	observer, _ := ctx.Value(stageObserverKey{}).(StageObserver)
	if observer == nil {
		return func(error, map[string]string) {}
	}
	started := time.Now()
	return func(err error, attrs map[string]string) {
		observer(ctx, StageReport{Stage: stage, Duration: time.Since(started), Err: err, Attrs: attrs})
	}
}

// Diagnostics is what a provider can tell an operator about a failed
// environment beyond the error itself.
type Diagnostics struct {
	// Summary is a few key facts (build status, agent lifecycle).
	Summary map[string]string
	// BuildLog and AgentLog are the tails of the provider's build log and the
	// in-workspace startup log, oldest line first.
	BuildLog []string
	AgentLog []string
}

// Diagnoser is an optional provider capability used when a sandbox fails to
// start, to capture the provider-side logs that explain why.
type Diagnoser interface {
	Diagnose(ctx context.Context, id ID) (Diagnostics, error)
}
