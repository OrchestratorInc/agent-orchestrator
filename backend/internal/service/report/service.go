package report

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// Store is the report service's persistence and ownership boundary.
type Store interface {
	GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error)
	CreateReport(context.Context, domain.ReportRecord) (domain.ReportRecord, error)
}

// CreateInput is the validated caller payload before AO-derived ownership and deadlines.
type CreateInput struct {
	SessionID domain.SessionID
	State     domain.ReportState
	Note      string
	Message   string
	Outputs   []domain.ReportOutput
}

// ArtifactRecorder shows a reported artifact in the worker's chat thread. It
// is best effort: it skips what it cannot show and never fails the report.
type ArtifactRecorder interface {
	RecordReportedArtifact(ctx context.Context, id domain.SessionID, reference string)
}

// CardSummaryFiller sets a session's kanban card summary only while it is empty
// or still previous, the text an earlier fill wrote, so it never replaces a
// summary the agent set.
type CardSummaryFiller interface {
	FillSessionCardSummary(ctx context.Context, id domain.SessionID, summary, previous string) (bool, error)
}

// Service validates and creates durable worker reports.
type Service struct {
	store         Store
	now           func() time.Time
	newID         func() string
	onCreated     func(domain.ReportRecord)
	artifacts     ArtifactRecorder
	cardSummaries CardSummaryFiller
}

// Deps configures a Service. Artifacts and CardSummaries are optional.
type Deps struct {
	Store         Store
	Now           func() time.Time
	NewID         func() string
	OnCreated     func(domain.ReportRecord)
	Artifacts     ArtifactRecorder
	CardSummaries CardSummaryFiller
}

// New constructs a report Service.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.NewID == nil {
		d.NewID = func() string { return "rpt_" + uuid.NewString() }
	}
	return &Service{store: d.Store, now: d.Now, newID: d.NewID, onCreated: d.OnCreated, artifacts: d.Artifacts, cardSummaries: d.CardSummaries}
}

// ListProject returns persisted report facts ordered by created_at then id,
// with each report's outputs in persisted position order. It never claims,
// acknowledges, schedules, or otherwise mutates report delivery state.
func (s *Service) ListProject(ctx context.Context, projectID domain.ProjectID) ([]domain.ReportRecord, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("report: store is required")
	}
	if projectID == "" {
		return nil, apierr.Invalid("REPORT_PROJECT_REQUIRED", "Project id is required", nil)
	}
	reader, ok := s.store.(interface {
		ListReportsByProject(context.Context, domain.ProjectID) ([]domain.ReportRecord, error)
	})
	if !ok {
		return nil, errors.New("report: project reader is required")
	}
	reports, err := reader.ListReportsByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(reports, func(i, j int) bool {
		if reports[i].CreatedAt.Equal(reports[j].CreatedAt) {
			return reports[i].ID < reports[j].ID
		}
		return reports[i].CreatedAt.Before(reports[j].CreatedAt)
	})
	return reports, nil
}

// Create validates ownership and persists one pending report. Reports are
// coordination claims and never mutate authoritative worker lifecycle state.
func (s *Service) Create(ctx context.Context, input CreateInput) (domain.ReportRecord, error) {
	if s == nil || s.store == nil {
		return domain.ReportRecord{}, errors.New("report: store is required")
	}
	if input.SessionID == "" {
		return domain.ReportRecord{}, apierr.Invalid("INVALID_REPORT_SESSION", "Worker session id is required", nil)
	}
	if err := domain.ValidateReportContent(input.State, input.Note, input.Message, input.Outputs); err != nil {
		return domain.ReportRecord{}, apierr.Invalid("INVALID_REPORT", "Report state, note, message, or outputs are invalid", nil)
	}
	session, ok, err := s.store.GetSession(ctx, input.SessionID)
	if err != nil {
		return domain.ReportRecord{}, err
	}
	if !ok {
		return domain.ReportRecord{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	if session.Kind != domain.KindWorker {
		return domain.ReportRecord{}, apierr.Invalid("REPORT_WORKER_REQUIRED", "Reports can only be created by worker sessions", nil)
	}

	now := s.now().UTC()
	var availableAt time.Time
	var settlementDeadline time.Time
	switch input.State {
	case domain.ReportDone:
		settlementDeadline = now.Add(domain.ReportSettlementWindow)
		availableAt = settlementDeadline
	case domain.ReportNeedsInput, domain.ReportStuck:
		availableAt = now
	default:
		availableAt = now.Add(domain.ReportBatchFallback)
	}
	rec := domain.ReportRecord{
		ID: s.newID(), SessionID: session.ID, ProjectID: session.ProjectID,
		State: input.State, Note: input.Note, Message: input.Message, Outputs: input.Outputs,
		CreatedAt: now, DeliveryState: domain.ReportPending, AvailableAt: availableAt,
		SettlementDeadline: settlementDeadline, RepeatCount: 1,
	}
	created, err := s.store.CreateReport(ctx, rec)
	if err != nil {
		return created, err
	}
	if s.onCreated != nil {
		s.onCreated(created)
	}
	// Synchronous on purpose: ao report --done is often the agent's last tool
	// call, so a goroutine would race the turn's completion and could drop the
	// artifact or put it on the next turn.
	if s.artifacts != nil {
		for _, output := range created.Outputs {
			if output.Kind == domain.ReportOutputArtifact {
				s.artifacts.RecordReportedArtifact(ctx, created.SessionID, output.Reference)
			}
		}
	}
	s.fillCardSummary(ctx, created)
	return created, nil
}

// fillCardSummary gives a finished worker's card its done report as a summary
// when the agent never set one with `ao summary`. Agents reliably file the
// done report but often skip the summary. Best effort: it never fails the
// report, and a summary the agent set is never replaced.
func (s *Service) fillCardSummary(ctx context.Context, report domain.ReportRecord) {
	if s.cardSummaries == nil || report.State != domain.ReportDone {
		return
	}
	if text := doneReportSummary(report); text != "" {
		_, _ = s.cardSummaries.FillSessionCardSummary(ctx, report.SessionID, text, s.previousDoneSummary(ctx, report))
	}
}

func doneReportSummary(report domain.ReportRecord) string {
	if text := domain.CardSummaryFromNote(report.Note); text != "" {
		return text
	}
	return domain.CardSummaryFromNote(report.Message)
}

// previousDoneSummary is the card text the session's previous done report
// would have filled in, so a newer done report can replace it. Empty when
// there is none or the reports cannot be read.
func (s *Service) previousDoneSummary(ctx context.Context, current domain.ReportRecord) string {
	reader, ok := s.store.(interface {
		ListReportsBySession(context.Context, domain.SessionID) ([]domain.ReportRecord, error)
	})
	if !ok {
		return ""
	}
	reports, err := reader.ListReportsBySession(ctx, current.SessionID)
	if err != nil {
		return ""
	}
	for i := len(reports) - 1; i >= 0; i-- {
		if reports[i].ID != current.ID && reports[i].State == domain.ReportDone {
			return doneReportSummary(reports[i])
		}
	}
	return ""
}
