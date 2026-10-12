package reconcile

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/activity"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
)

// ActivityRecorder receives support-trail events. It must never block.
type ActivityRecorder interface {
	Record(context.Context, domain.ActivityEvent)
}

// SetActivityRecorder routes lifecycle events (provisioned, paused, start
// failures, bootstrap stages) to the organization's support trail.
func (r *Reconciler) SetActivityRecorder(recorder ActivityRecorder) {
	r.activity = recorder
}

// startupDetailStore is the optional store capability that persists the
// operator detail behind a startup error.
type startupDetailStore interface {
	RecordSandboxStartupErrorDetail(ctx context.Context, owner, orgID, sessionID, detail string) error
}

const (
	// diagnoseTimeout bounds the provider log fetch on a startup failure.
	diagnoseTimeout = 15 * time.Second
	// startupDetailLimit matches ao_sandboxes.startup_error_detail.
	startupDetailLimit = 4000
)

func (r *Reconciler) recordActivity(
	ctx context.Context,
	record domain.Sandbox,
	kind string,
	attrs map[string]any,
) {
	if r.activity == nil {
		return
	}
	if record.ProviderConnectionID != "" {
		if attrs == nil {
			attrs = map[string]any{}
		}
		attrs["connection_id"] = record.ProviderConnectionID
	}
	r.activity.Record(ctx, domain.ActivityEvent{
		OrgID:     record.OrgID,
		SessionID: record.SessionID,
		Kind:      kind,
		Provider:  record.Provider,
		Attrs:     attrs,
	})
}

// stageTracker remembers the last provider stage that failed within one
// reconcile claim, so a startup error can name the stage it came from.
type stageTracker struct {
	mu         sync.Mutex
	lastFailed string
}

type stageTrackerKey struct{}

func failedStage(ctx context.Context) string {
	tracker, _ := ctx.Value(stageTrackerKey{}).(*stageTracker)
	if tracker == nil {
		return ""
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	return tracker.lastFailed
}

// observeStages installs a provider stage observer for one claim: each stage
// a provider reports (Coder: workspace create/start, agent wait, durable root
// probe, worker install and launch) is logged with its duration, workspace and
// connection, and recorded on the support trail. A "not ready yet" agent wait
// is retried every few seconds while a template's startup script runs, so it
// is logged at debug and kept off the trail.
func (r *Reconciler) observeStages(ctx context.Context, record domain.Sandbox) context.Context {
	tracker := &stageTracker{}
	ctx = context.WithValue(ctx, stageTrackerKey{}, tracker)
	return sandbox.WithStageObserver(ctx, func(ctx context.Context, report sandbox.StageReport) {
		notReady := errors.Is(report.Err, sandbox.ErrWorkspaceNotReady)
		args := []any{
			"stage", report.Stage,
			"duration_ms", report.Duration.Milliseconds(),
			"session_id", record.SessionID,
		}
		keys := make([]string, 0, len(report.Attrs))
		for key := range report.Attrs {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			args = append(args, key, report.Attrs[key])
		}
		switch {
		case report.Err == nil:
			r.log.InfoContext(ctx, "provider stage complete", args...)
		case notReady:
			r.log.DebugContext(ctx, "provider stage not ready", append(args, "err", report.Err)...)
		default:
			tracker.mu.Lock()
			tracker.lastFailed = report.Stage
			tracker.mu.Unlock()
			r.log.WarnContext(ctx, "provider stage failed",
				append(args, "err", activity.Excerpt(report.Err.Error(), 2048))...)
		}
		if notReady {
			return
		}
		attrs := map[string]any{
			"stage":       report.Stage,
			"duration_ms": report.Duration.Milliseconds(),
			"ok":          report.Err == nil,
		}
		for key, value := range report.Attrs {
			attrs[key] = value
		}
		if report.Err != nil {
			var startupErr *sandbox.StartupError
			if errors.As(report.Err, &startupErr) {
				attrs["code"] = startupErr.Code
			}
			attrs["error"] = activity.Excerpt(report.Err.Error(), 1024)
		}
		r.recordActivity(ctx, record, domain.ActivityBootstrapStage, attrs)
	})
}

// startupFailed runs once per new startup error code for a session: it logs
// the failure with its operator detail, captures provider-side logs (the
// Coder build and agent startup logs) into the sandbox's startup_error_detail,
// and records session.start_failed on the support trail.
func (r *Reconciler) startupFailed(
	ctx context.Context,
	record domain.Sandbox,
	startupErr *sandbox.StartupError,
) {
	stage := failedStage(ctx)
	cause := ""
	if startupErr.Err != nil {
		cause = startupErr.Err.Error()
	}
	diagnostics, diagnoseErr := r.diagnose(ctx, record)
	detail := startupDetail(startupErr, stage, cause, diagnostics, diagnoseErr)
	if store, ok := r.store.(startupDetailStore); ok && detail != "" {
		if err := store.RecordSandboxStartupErrorDetail(
			ctx, r.owner, record.OrgID, record.SessionID, detail,
		); err != nil && !errors.Is(err, postgres.ErrSandboxLeaseLost) {
			r.log.WarnContext(ctx, "record sandbox startup error detail", "session_id", record.SessionID, "err", err)
		}
	}
	args := []any{
		"session_id", record.SessionID,
		"startup_error_code", startupErr.Code,
		"failed_stage", stage,
		"startup_attempts", record.StartupAttempts,
		"customer_owned", customerOwnedCompute(record),
		"detail", detail,
	}
	if diagnoseErr != nil {
		args = append(args, "diagnose_error", activity.Excerpt(diagnoseErr.Error(), 512))
	}
	r.log.WarnContext(ctx, "sandbox startup failed", args...)

	attrs := map[string]any{
		"code":             startupErr.Code,
		"message":          startupErr.Message,
		"startup_attempts": record.StartupAttempts,
		"cause":            activity.Excerpt(cause, 1024),
		"customer_owned":   customerOwnedCompute(record),
	}
	if stage != "" {
		attrs["stage"] = stage
	}
	if record.ProviderEnvironmentID != "" {
		attrs["provider_environment_id"] = record.ProviderEnvironmentID
	}
	for _, key := range []string{"job_error", "job_error_code", "agent_lifecycle", "build_status", "coder_template", "coder_owner", "worker_user", "checkout_reuse", "ready_probe"} {
		if value := diagnostics.Summary[key]; value != "" {
			attrs[key] = value
		}
	}
	if len(diagnostics.BuildLog) > 0 {
		attrs["build_log_tail"] = activity.TailLines(diagnostics.BuildLog, 15, 1500)
	}
	if len(diagnostics.AgentLog) > 0 {
		attrs["agent_log_tail"] = activity.TailLines(diagnostics.AgentLog, 15, 1500)
	}
	r.recordActivity(ctx, record, domain.ActivitySessionStartFailed, attrs)
}

func (r *Reconciler) diagnose(ctx context.Context, record domain.Sandbox) (sandbox.Diagnostics, error) {
	if record.ProviderEnvironmentID == "" {
		return sandbox.Diagnostics{}, nil
	}
	provider, err := r.providers.Resolve(ctx, record)
	if err != nil {
		return sandbox.Diagnostics{}, err
	}
	diagnoser, ok := provider.(sandbox.Diagnoser)
	if !ok {
		return sandbox.Diagnostics{}, nil
	}
	diagnoseCtx, cancel := context.WithTimeout(ctx, diagnoseTimeout)
	defer cancel()
	return diagnoser.Diagnose(diagnoseCtx, sandbox.ID(record.ProviderEnvironmentID))
}

// startupDetail renders the operator detail for a startup error: the code,
// the failed stage, the redacted cause (which for a Coder bootstrap carries
// the tail of the bootstrap script's terminal output), the provider summary
// and the log tails. The result is bounded and redacted.
func startupDetail(
	startupErr *sandbox.StartupError,
	stage, cause string,
	diagnostics sandbox.Diagnostics,
	diagnoseErr error,
) string {
	var builder strings.Builder
	builder.WriteString("code: " + startupErr.Code + "\n")
	if stage != "" {
		builder.WriteString("stage: " + stage + "\n")
	}
	if cause != "" {
		builder.WriteString("cause: " + activity.Excerpt(cause, 1200) + "\n")
	}
	if len(diagnostics.Summary) > 0 {
		keys := make([]string, 0, len(diagnostics.Summary))
		for key := range diagnostics.Summary {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if value := diagnostics.Summary[key]; value != "" {
				builder.WriteString(key + ": " + activity.Excerpt(value, 300) + "\n")
			}
		}
	}
	if diagnoseErr != nil {
		builder.WriteString("diagnose_error: " + activity.Excerpt(diagnoseErr.Error(), 300) + "\n")
	}
	if len(diagnostics.BuildLog) > 0 {
		builder.WriteString("--- build log (tail) ---\n" + activity.TailLines(diagnostics.BuildLog, 20, 1000) + "\n")
	}
	if len(diagnostics.AgentLog) > 0 {
		builder.WriteString("--- agent startup log (tail) ---\n" + activity.TailLines(diagnostics.AgentLog, 20, 1000) + "\n")
	}
	return activity.Truncate(strings.TrimRight(builder.String(), "\n"), startupDetailLimit)
}
