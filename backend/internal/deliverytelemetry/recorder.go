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
	"github.com/aoagents/agent-orchestrator/backend/internal/telemetrymeta"
)

type Store interface {
	UsageProjectID(ctx context.Context, projectID, installID string) (string, error)
	GetSessionTelemetry(ctx context.Context, id domain.SessionID) (domain.SessionTelemetryFacts, error)
	ListPRsBySession(ctx context.Context, id domain.SessionID) ([]domain.PullRequest, error)
	ListReportsBySession(ctx context.Context, id domain.SessionID) ([]domain.ReportRecord, error)
	ClaimSessionEndTelemetry(ctx context.Context, id domain.SessionID) (bool, error)
}

type EndReason string

const (
	EndKilled      EndReason = "killed"
	EndPRMerged    EndReason = "pr_merged"
	EndIssueClosed EndReason = "issue_closed"
	EndRuntimeDied EndReason = "runtime_died"
)

const (
	OutcomePROpened  = "pr_opened"
	OutcomeCompleted = "completed"
	OutcomeKilled    = "killed"
	OutcomeFailed    = "failed"
	OutcomeAbandoned = "abandoned"
)

const (
	MergedViaDesktop = "desktop"
	MergedViaMobile  = "mobile"
	MergedViaCLI     = "cli"
	MergedViaGitHub  = "github"
)

type Recorder struct {
	sink  ports.EventSink
	store Store
	now   func() time.Time

	installMu sync.RWMutex
	installID func() string
}

func (r *Recorder) SetInstallID(installID func() string) {
	if r == nil {
		return
	}
	r.installMu.Lock()
	r.installID = installID
	r.installMu.Unlock()
}

func SessionTelemetryID(projectTelemetryID, sessionID string) string {
	key := sha256.Sum256([]byte(projectTelemetryID + ":" + sessionID))
	return hex.EncodeToString(key[:16])
}

func New(sink ports.EventSink, store Store) *Recorder {
	return &Recorder{sink: sink, store: store, now: time.Now}
}

func (r *Recorder) enabled() bool { return r != nil && r.sink != nil && r.store != nil }

func (r *Recorder) SessionEnded(ctx context.Context, rec domain.SessionRecord, reason EndReason) {
	if !r.enabled() || rec.ID == "" {
		return
	}
	readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	prs, err := r.store.ListPRsBySession(readCtx, rec.ID)
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
	if reports, err := r.store.ListReportsBySession(readCtx, rec.ID); err == nil {
		for _, report := range reports {
			if report.State == domain.ReportDone {
				doneReported = true
				break
			}
		}
	}
	facts, _ := r.store.GetSessionTelemetry(readCtx, rec.ID)
	payload := map[string]any{
		"kind":                telemetrymeta.SessionKind(rec.Kind, rec.ProjectID),
		"harness":             string(rec.Harness),
		"outcome":             sessionOutcome(reason, rec.Activity.State, hadPR, doneReported),
		"had_pr":              hadPR,
		"waiting_input_count": facts.WaitingInputCount,
	}
	if !rec.CreatedAt.IsZero() {
		payload["duration_ms"] = nonNegative(r.now().Sub(rec.CreatedAt).Milliseconds())
	}
	if facts.CreatedBy.Valid() {
		payload["created_by"] = string(facts.CreatedBy)
	}
	r.stampUsageGrouping(readCtx, rec, payload)
	claimed, err := r.store.ClaimSessionEndTelemetry(readCtx, rec.ID)
	if err != nil || !claimed {
		return
	}
	r.emit(ctx, "ao.sessions.ended", rec, payload)
}

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

func MergedViaSurface(surface domain.RequestSurface, ok bool) string {
	if !ok {
		return MergedViaCLI
	}
	switch surface {
	case domain.RequestSurfaceDesktop:
		return MergedViaDesktop
	case domain.RequestSurfaceMobile:
		return MergedViaMobile
	default:
		return MergedViaCLI
	}
}

func MergedOutsideAO(provider string) string {
	if provider == "github" {
		return MergedViaGitHub
	}
	return ""
}

func (r *Recorder) stampUsageGrouping(ctx context.Context, rec domain.SessionRecord, payload map[string]any) {
	r.installMu.RLock()
	installIDFn := r.installID
	r.installMu.RUnlock()
	if installIDFn == nil {
		return
	}
	installID := installIDFn()
	if installID == "" {
		return
	}
	if rec.ProjectID == "" {
		payload["session_telemetry_id"] = SessionTelemetryID("install:"+installID, string(rec.ID))
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
