package report

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// WorkerReporterStore is what the worker reporter reads to build a report.
type WorkerReporterStore interface {
	GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error)
	ListReportsByProject(context.Context, domain.ProjectID) ([]domain.ReportRecord, error)
	ConversationForSession(context.Context, domain.SessionID) (domain.ConversationRecord, error)
	ConversationMessages(context.Context, string) ([]domain.ConversationMessage, error)
}

// ReportCreator persists one report, as Service.Create does.
type ReportCreator interface {
	Create(context.Context, CreateInput) (domain.ReportRecord, error)
}

// WorkerReporter writes worker reports from the signals the daemon already
// observes, so agents do not have to restate them with `ao report` (ADR 0007):
//
//   - active then idle: done, with the worker's final assistant message
//   - active then waiting_input or blocked: needs_input
//   - active then exited, without termination: stuck
//
// Only transitions out of observed live work produce a report. A session the
// daemon never saw active since it started is left alone, and so is a killed
// session. While `ao report` still exists, a report the agent filed itself
// during the same stretch of work takes precedence and nothing is added.
type WorkerReporter struct {
	store   WorkerReporterStore
	reports ReportCreator
	now     func() time.Time
	log     *slog.Logger

	mu          sync.Mutex
	activeSince map[domain.SessionID]time.Time
}

// WorkerReporterDeps configures a WorkerReporter. Now and Logger are optional.
type WorkerReporterDeps struct {
	Store   WorkerReporterStore
	Reports ReportCreator
	Now     func() time.Time
	Logger  *slog.Logger
}

// NewWorkerReporter constructs an idle reporter.
func NewWorkerReporter(d WorkerReporterDeps) *WorkerReporter {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return &WorkerReporter{
		store: d.Store, reports: d.Reports, now: d.Now, log: d.Logger,
		activeSince: make(map[domain.SessionID]time.Time),
	}
}

// Transition records one session activity change. It returns the report state
// it will write, or "" when the change needs no report. Callers run Write with
// the returned values off the event path.
func (r *WorkerReporter) Transition(id domain.SessionID, activity domain.ActivityState, terminated bool) (domain.ReportState, time.Time) {
	if r == nil || id == "" {
		return "", time.Time{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if activity == domain.ActivityActive && !terminated {
		if _, ok := r.activeSince[id]; !ok {
			r.activeSince[id] = r.now().UTC()
		}
		return "", time.Time{}
	}
	since, ok := r.activeSince[id]
	if !ok {
		return "", time.Time{}
	}
	if terminated {
		delete(r.activeSince, id)
		return "", time.Time{}
	}
	var state domain.ReportState
	switch activity {
	case domain.ActivityIdle:
		state = domain.ReportDone
	case domain.ActivityWaitingInput, domain.ActivityBlocked:
		state = domain.ReportNeedsInput
	case domain.ActivityExited:
		state = domain.ReportStuck
	default:
		return "", time.Time{}
	}
	delete(r.activeSince, id)
	return state, since
}

// Write persists the report chosen by Transition for a project worker. It is
// best effort: failures are logged and never affect the session.
func (r *WorkerReporter) Write(ctx context.Context, id domain.SessionID, state domain.ReportState, since time.Time) {
	if r == nil || state == "" {
		return
	}
	session, ok, err := r.store.GetSession(ctx, id)
	if err != nil || !ok || session.Kind != domain.KindWorker || session.ProjectID == "" {
		return
	}
	if r.agentReported(ctx, session, since) {
		return
	}
	input := CreateInput{SessionID: id, State: state, Note: r.note(ctx, id, state, since)}
	if _, err := r.reports.Create(ctx, input); err != nil {
		r.log.Warn("worker report: create failed", "session", id, "state", state, "err", err)
	}
}

// agentReported reports whether the worker filed its own report during this
// stretch of work. It goes away with `ao report`.
func (r *WorkerReporter) agentReported(ctx context.Context, session domain.SessionRecord, since time.Time) bool {
	reports, err := r.store.ListReportsByProject(ctx, session.ProjectID)
	if err != nil {
		return false
	}
	for _, report := range reports {
		if report.SessionID == session.ID && !report.CreatedAt.Before(since) {
			return true
		}
	}
	return false
}

func (r *WorkerReporter) note(ctx context.Context, id domain.SessionID, state domain.ReportState, since time.Time) string {
	switch state {
	case domain.ReportNeedsInput:
		return "Waiting for an approval or an answer in its session."
	case domain.ReportStuck:
		return "The agent process exited before finishing."
	}
	if text := r.finalMessage(ctx, id, since); text != "" {
		return text
	}
	return "Finished its turn. Read `ao session history " + string(id) + "` for the result."
}

// finalMessage returns the worker's last assistant message from this stretch
// of work, the subagent-style result. Terminal sessions have none.
func (r *WorkerReporter) finalMessage(ctx context.Context, id domain.SessionID, since time.Time) string {
	conversation, err := r.store.ConversationForSession(ctx, id)
	if err != nil || conversation.ID == "" {
		return ""
	}
	messages, err := r.store.ConversationMessages(ctx, conversation.ID)
	if err != nil {
		return ""
	}
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.CreatedAt.Before(since) {
			break
		}
		if msg.Role != domain.MessageRoleAssistant {
			continue
		}
		if text := strings.TrimSpace(msg.Text); text != "" {
			return truncateRunes(text, domain.MaxReportTextCharacters)
		}
	}
	return ""
}

func truncateRunes(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit-1]) + "…"
}
