// Package deliverytelemetry emits ao.session.ended, which says how a session
// ended and whether it delivered. Several owners end sessions (session kill,
// lifecycle teardown, the reaper), so the payload is built here once. PR
// outcomes are measured by the usage flusher (ao.usage.worker_pr_*); this
// package only names how a merge happened, for that measurement.
//
// Payloads carry enums, counts and durations only: never a title, branch, URL,
// path or message text.
package deliverytelemetry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/reqid"
)

// Store is the read surface the payloads are derived from.
type Store interface {
	// UsageProjectID is the random per-project grouping ID the usage summaries
	// (ao.usage.*) carry, so an ended session joins its message counts.
	UsageProjectID(ctx context.Context, projectID, installID string) (string, error)
	GetSessionTelemetry(ctx context.Context, id domain.SessionID) (domain.SessionTelemetryFacts, error)
	ListPRsBySession(ctx context.Context, id domain.SessionID) ([]domain.PullRequest, error)
	ListReportsBySession(ctx context.Context, id domain.SessionID) ([]domain.ReportRecord, error)
}

// EndReason is what ended a session, as known by the code that ended it.
type EndReason string

// End reasons.
const (
	// EndKilled is an explicit kill from a person or an agent.
	EndKilled EndReason = "killed"
	// EndPRMerged is the teardown after the session's PRs merged.
	EndPRMerged EndReason = "pr_merged"
	// EndIssueClosed is the teardown after the tracker issue was closed.
	EndIssueClosed EndReason = "issue_closed"
	// EndRuntimeDied is the reaper finding the runtime conclusively dead.
	EndRuntimeDied EndReason = "runtime_died"
)

// Session outcomes reported on ao.session.ended.
const (
	OutcomePROpened  = "pr_opened"
	OutcomeCompleted = "completed"
	OutcomeKilled    = "killed"
	OutcomeFailed    = "failed"
	OutcomeAbandoned = "abandoned"
)

// How a merge happened, reported as merged_via on ao.pr.merged.
const (
	MergedViaDesktop     = "desktop"
	MergedViaMobileSwipe = "mobile_swipe"
	MergedViaGitHub      = "github"
	MergedViaOther       = "other"
)

// Recorder emits delivery events. A nil Recorder, or one without a sink, does
// nothing, so optional wiring stays optional.
type Recorder struct {
	sink  ports.EventSink
	store Store
	now   func() time.Time

	installMu sync.RWMutex
	installID func() string
}

// SetInstallID supplies the install ID the usage grouping IDs are scoped to.
// It returns "" while export is off, and the grouping IDs are then omitted.
func (r *Recorder) SetInstallID(installID func() string) {
	if r == nil {
		return
	}
	r.installMu.Lock()
	r.installID = installID
	r.installMu.Unlock()
}

// SessionTelemetryID is the session grouping value ao.usage.messages_sent
// carries: derived from the random project grouping ID and the local session
// ID, so it groups one session's batches without exposing the session ID.
func SessionTelemetryID(projectTelemetryID, sessionID string) string {
	key := sha256.Sum256([]byte(projectTelemetryID + ":" + sessionID))
	return hex.EncodeToString(key[:16])
}

// New returns a Recorder.
func New(sink ports.EventSink, store Store) *Recorder {
	return &Recorder{sink: sink, store: store, now: time.Now}
}

func (r *Recorder) enabled() bool { return r != nil && r.sink != nil && r.store != nil }

// SessionEnded reports a session that has just been terminated. rec is the
// record as it was before termination, so the activity it was in when it ended
// is still visible.
func (r *Recorder) SessionEnded(ctx context.Context, rec domain.SessionRecord, reason EndReason) {
	if !r.enabled() || rec.ID == "" {
		return
	}
	prs, err := r.store.ListPRsBySession(ctx, rec.ID)
	if err != nil {
		return
	}
	hadPR := false
	for _, pr := range prs {
		if pr.Number > 0 {
			hadPR = true
			break
		}
	}
	doneReported := false
	if reports, err := r.store.ListReportsBySession(ctx, rec.ID); err == nil {
		for _, report := range reports {
			if report.State == domain.ReportDone {
				doneReported = true
				break
			}
		}
	}
	facts, _ := r.store.GetSessionTelemetry(ctx, rec.ID)
	payload := map[string]any{
		"kind":                string(rec.Kind),
		"harness":             string(rec.Harness),
		"outcome":             sessionOutcome(reason, rec.Activity.State, hadPR, doneReported),
		"had_pr":              hadPR,
		"waiting_input_count": facts.WaitingInputCount,
	}
	if !rec.CreatedAt.IsZero() {
		payload["duration_s"] = nonNegative(int64(r.now().Sub(rec.CreatedAt) / time.Second))
	}
	if facts.CreatedBy.Valid() {
		payload["created_by"] = string(facts.CreatedBy)
	}
	r.stampUsageGrouping(ctx, rec, payload)
	r.emit(ctx, "ao.session.ended", rec, payload)
}

// sessionOutcome folds how a session ended into one outcome. A PR wins over
// everything but a crash, because a PR is the deliverable; a done report (or a
// closed issue) without a PR is a completed non-PR task. An explicit kill with
// nothing delivered is "killed" while the agent was mid-turn and "abandoned"
// when it had already stopped: idle, waiting on the user, or exited.
func sessionOutcome(reason EndReason, activity domain.ActivityState, hadPR, doneReported bool) string {
	switch {
	case reason == EndRuntimeDied:
		return OutcomeFailed
	case reason == EndPRMerged || hadPR:
		return OutcomePROpened
	case reason == EndIssueClosed || doneReported:
		return OutcomeCompleted
	case activity == domain.ActivityActive:
		return OutcomeKilled
	default:
		return OutcomeAbandoned
	}
}

// MergedViaSurface maps the client that asked AO to merge to merged_via. The
// mobile app merges with a swipe on the PR card.
func MergedViaSurface(surface domain.RequestSurface, ok bool) string {
	if !ok {
		return MergedViaOther
	}
	switch surface {
	case domain.RequestSurfaceDesktop:
		return MergedViaDesktop
	case domain.RequestSurfaceMobile:
		return MergedViaMobileSwipe
	default:
		return MergedViaOther
	}
}

// MergedOutsideAO is merged_via for a merge AO only observed: done on the
// provider's own site (or by another tool).
func MergedOutsideAO(provider string) string {
	if provider == "github" {
		return MergedViaGitHub
	}
	return MergedViaOther
}

// stampUsageGrouping adds the same project and session grouping IDs the usage
// summaries carry. Standalone sessions have no project grouping.
func (r *Recorder) stampUsageGrouping(ctx context.Context, rec domain.SessionRecord, payload map[string]any) {
	r.installMu.RLock()
	installIDFn := r.installID
	r.installMu.RUnlock()
	if installIDFn == nil || rec.ProjectID == "" {
		return
	}
	installID := installIDFn()
	if installID == "" {
		return
	}
	projectTelemetryID, err := r.store.UsageProjectID(ctx, string(rec.ProjectID), installID)
	if err != nil || projectTelemetryID == "" {
		return
	}
	payload["project_telemetry_id"] = projectTelemetryID
	payload["session_telemetry_id"] = SessionTelemetryID(projectTelemetryID, string(rec.ID))
}

func (r *Recorder) emit(ctx context.Context, name string, rec domain.SessionRecord, payload map[string]any) {
	sessionID := rec.ID
	ev := ports.TelemetryEvent{
		Name:       name,
		Source:     "delivery_telemetry",
		OccurredAt: r.now(),
		Level:      ports.TelemetryLevelInfo,
		SessionID:  &sessionID,
		RequestID:  reqid.FromContext(ctx),
		Payload:    payload,
	}
	if rec.ProjectID != "" {
		projectID := rec.ProjectID
		ev.ProjectID = &projectID
	}
	r.sink.Emit(context.WithoutCancel(ctx), ev)
}

func nonNegative(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}
