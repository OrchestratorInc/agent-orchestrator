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

// DoneQuietPeriod is how long a worker must stay idle before its done report
// is written. A harness that reports idle between steps, or a worker that
// picks up a queued message, usually resumes within it, so its idle gaps add
// no extra done reports.
const DoneQuietPeriod = 30 * time.Second

// WorkerReporter writes worker reports from the signals the daemon already
// observes, so agents do not have to restate them with `ao report` (ADR 0007):
//
//   - active then idle for DoneQuietPeriod: done, with the worker's final
//     assistant message
//   - active then waiting_input or blocked: needs_input
//   - active then exited, without termination: stuck
//
// Only transitions out of observed live work produce a report. A session the
// daemon never saw active since it started is left alone, and so is a killed
// session. Work that resumes during the quiet period, by event time and before
// the done timer fires, continues the same task, so its idle gaps add no extra
// done reports. While `ao report` still
// exists, a report the agent filed itself during the same stretch of work
// takes precedence and nothing is added.
type WorkerReporter struct {
	store     WorkerReporterStore
	reports   ReportCreator
	now       func() time.Time
	log       *slog.Logger
	quiet     time.Duration
	afterFunc func(time.Duration, func()) Stopper
	spawn     func(func())

	mu          sync.Mutex
	activeSince map[domain.SessionID]time.Time
	pendingDone map[domain.SessionID]pendingDone
	nextGen     uint64
	// own holds the IDs of reports this reporter wrote, so they are never
	// mistaken for a report the agent filed itself. It goes away with
	// `ao report`. It has its own lock because writes can run while mu is held.
	ownMu sync.Mutex
	own   map[string]struct{}
	// writeMu serializes Write, so a report another Write has just persisted
	// is registered in own before any other Write checks provenance.
	writeMu sync.Mutex
}

// Stretch is one stretch of observed work: from the event that made the
// worker active to the event that ended it, in event (not delivery) time.
type Stretch struct {
	Since time.Time
	Until time.Time
}

// Stopper cancels a scheduled function, as *time.Timer does.
type Stopper interface{ Stop() bool }

type pendingDone struct {
	stretch Stretch
	gen     uint64
	timer   Stopper
}

// WorkerReporterDeps configures a WorkerReporter. Only Store and Reports are
// required; the rest exist for tests.
type WorkerReporterDeps struct {
	Store       WorkerReporterStore
	Reports     ReportCreator
	Now         func() time.Time
	Logger      *slog.Logger
	QuietPeriod time.Duration
	AfterFunc   func(time.Duration, func()) Stopper
	Spawn       func(func())
}

// NewWorkerReporter constructs an idle reporter.
func NewWorkerReporter(d WorkerReporterDeps) *WorkerReporter {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.QuietPeriod <= 0 {
		d.QuietPeriod = DoneQuietPeriod
	}
	if d.AfterFunc == nil {
		d.AfterFunc = func(wait time.Duration, f func()) Stopper { return time.AfterFunc(wait, f) }
	}
	if d.Spawn == nil {
		d.Spawn = func(f func()) { go f() }
	}
	return &WorkerReporter{
		store: d.Store, reports: d.Reports, now: d.Now, log: d.Logger,
		quiet: d.QuietPeriod, afterFunc: d.AfterFunc, spawn: d.Spawn,
		activeSince: make(map[domain.SessionID]time.Time),
		pendingDone: make(map[domain.SessionID]pendingDone),
		own:         make(map[string]struct{}),
	}
}

// Observe records one session activity change, in feed order. at is when the
// change happened; a zero at uses the current time. Decisions are made under
// the lock; store reads and writes run off the caller's path.
func (r *WorkerReporter) Observe(ctx context.Context, id domain.SessionID, activity domain.ActivityState, terminated bool, at time.Time) {
	if r == nil || id == "" {
		return
	}
	if at.IsZero() {
		at = r.now()
	}
	at = at.UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	if terminated {
		r.cancelDoneLocked(id)
		delete(r.activeSince, id)
		return
	}
	switch activity {
	case domain.ActivityActive:
		if pending, ok := r.cancelDoneLocked(id); ok {
			if at.Sub(pending.stretch.Until) <= r.quiet {
				// Resumed within the quiet period, in event time: same task.
				r.activeSince[id] = pending.stretch.Since
				return
			}
			// The timer was cancelled only because delivery lagged; in event
			// time the worker was quiet long enough, so its task is done.
			stretch := pending.stretch
			r.spawn(func() { r.Write(ctx, id, domain.ReportDone, stretch) })
		}
		if _, ok := r.activeSince[id]; !ok {
			r.activeSince[id] = at
		}
	case domain.ActivityIdle:
		since, ok := r.activeSince[id]
		if !ok {
			return
		}
		delete(r.activeSince, id)
		r.nextGen++
		gen := r.nextGen
		timer := r.afterFunc(r.quiet, func() { r.fireDone(ctx, id, gen) })
		r.pendingDone[id] = pendingDone{stretch: Stretch{Since: since, Until: at}, gen: gen, timer: timer}
	case domain.ActivityWaitingInput, domain.ActivityBlocked:
		since, ok := r.activeSince[id]
		if pending, wasDone := r.cancelDoneLocked(id); wasDone {
			// A question right after finishing replaces the done report.
			since, ok = pending.stretch.Since, true
		}
		if !ok {
			return
		}
		delete(r.activeSince, id)
		stretch := Stretch{Since: since, Until: at}
		r.spawn(func() { r.Write(ctx, id, domain.ReportNeedsInput, stretch) })
	case domain.ActivityExited:
		// An exit after a finished turn leaves its pending done in place.
		since, ok := r.activeSince[id]
		if !ok {
			return
		}
		delete(r.activeSince, id)
		stretch := Stretch{Since: since, Until: at}
		r.spawn(func() { r.Write(ctx, id, domain.ReportStuck, stretch) })
	}
}

func (r *WorkerReporter) cancelDoneLocked(id domain.SessionID) (pendingDone, bool) {
	pending, ok := r.pendingDone[id]
	if !ok {
		return pendingDone{}, false
	}
	pending.timer.Stop()
	delete(r.pendingDone, id)
	return pending, true
}

func (r *WorkerReporter) fireDone(ctx context.Context, id domain.SessionID, gen uint64) {
	r.mu.Lock()
	pending, ok := r.pendingDone[id]
	if !ok || pending.gen != gen {
		r.mu.Unlock()
		return
	}
	delete(r.pendingDone, id)
	r.mu.Unlock()
	r.Write(ctx, id, domain.ReportDone, pending.stretch)
}

// Write persists one report for a project worker. It is
// best effort: failures are logged and never affect the session.
func (r *WorkerReporter) Write(ctx context.Context, id domain.SessionID, state domain.ReportState, stretch Stretch) {
	if r == nil || state == "" {
		return
	}
	session, ok, err := r.store.GetSession(ctx, id)
	if err != nil || !ok || session.Kind != domain.KindWorker || session.ProjectID == "" || session.IsTerminated {
		return
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	if r.agentReported(ctx, session, stretch) {
		return
	}
	input := CreateInput{SessionID: id, State: state, Note: r.note(ctx, id, state, stretch)}
	created, err := r.reports.Create(ctx, input)
	if err != nil {
		r.log.Warn("worker report: create failed", "session", id, "state", state, "err", err)
		return
	}
	if created.ID != "" {
		r.ownMu.Lock()
		r.own[created.ID] = struct{}{}
		r.ownMu.Unlock()
	}
}

// agentReported reports whether the worker filed its own report during this
// stretch of work. It goes away with `ao report`.
func (r *WorkerReporter) agentReported(ctx context.Context, session domain.SessionRecord, stretch Stretch) bool {
	reports, err := r.store.ListReportsByProject(ctx, session.ProjectID)
	if err != nil {
		return false
	}
	for _, report := range reports {
		if report.SessionID != session.ID || report.CreatedAt.Before(stretch.Since) || report.CreatedAt.After(stretch.Until) {
			continue
		}
		r.ownMu.Lock()
		_, mine := r.own[report.ID]
		r.ownMu.Unlock()
		if !mine {
			return true
		}
	}
	return false
}

func (r *WorkerReporter) note(ctx context.Context, id domain.SessionID, state domain.ReportState, stretch Stretch) string {
	switch state {
	case domain.ReportNeedsInput:
		return "Waiting for an approval or an answer in its session."
	case domain.ReportStuck:
		return "The agent process exited before finishing."
	}
	if text := r.finalMessage(ctx, id, stretch); text != "" {
		return text
	}
	return "Finished its turn. Read `ao session history " + string(id) + "` for the result."
}

// finalMessage returns the worker's last assistant message from this stretch
// of work, the subagent-style result. Terminal sessions have none.
func (r *WorkerReporter) finalMessage(ctx context.Context, id domain.SessionID, stretch Stretch) string {
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
		if msg.CreatedAt.After(stretch.Until) {
			// A later turn's answer; this report covers only its own stretch.
			continue
		}
		if msg.CreatedAt.Before(stretch.Since) {
			break
		}
		if msg.Role != domain.MessageRoleAssistant {
			continue
		}
		if text := strings.TrimSpace(msg.Text); text != "" {
			return fitNote(text, id)
		}
	}
	return ""
}

// fitNote keeps a final message within the report limit. A message that is
// too long keeps its beginning and says where to read the rest, so a question
// at its end is never silently lost.
func fitNote(text string, id domain.SessionID) string {
	limit := domain.MaxReportTextCharacters
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	suffix := "… [Truncated. Read the full message with `ao session history " + string(id) + "`.]"
	keep := limit - utf8.RuneCountInString(suffix)
	return strings.TrimSpace(string([]rune(text)[:keep])) + suffix
}
