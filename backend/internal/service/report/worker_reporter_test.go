package report

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type fakeReporterStore struct {
	sessions     map[domain.SessionID]domain.SessionRecord
	reports      []domain.ReportRecord
	conversation map[domain.SessionID]string
	messages     map[string][]domain.ConversationMessage
}

func (f *fakeReporterStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	s, ok := f.sessions[id]
	return s, ok, nil
}

func (f *fakeReporterStore) ListReportsByProject(_ context.Context, _ domain.ProjectID) ([]domain.ReportRecord, error) {
	return f.reports, nil
}

func (f *fakeReporterStore) ConversationForSession(_ context.Context, id domain.SessionID) (domain.ConversationRecord, error) {
	conv, ok := f.conversation[id]
	if !ok {
		return domain.ConversationRecord{}, errors.New("not found")
	}
	return domain.ConversationRecord{ID: conv}, nil
}

func (f *fakeReporterStore) ConversationMessages(_ context.Context, id string) ([]domain.ConversationMessage, error) {
	return f.messages[id], nil
}

type fakeCreator struct{ created []CreateInput }

func (f *fakeCreator) Create(_ context.Context, in CreateInput) (domain.ReportRecord, error) {
	f.created = append(f.created, in)
	return domain.ReportRecord{ID: fmt.Sprintf("auto-%d", len(f.created)), SessionID: in.SessionID}, nil
}

type fakeTimer struct {
	f       func()
	stopped bool
}

func (t *fakeTimer) Stop() bool {
	was := !t.stopped
	t.stopped = true
	return was
}

type fakeClock struct{ timers []*fakeTimer }

func (c *fakeClock) AfterFunc(_ time.Duration, f func()) Stopper {
	timer := &fakeTimer{f: f}
	c.timers = append(c.timers, timer)
	return timer
}

// elapse runs every scheduled function that was not stopped, as if the quiet
// period passed.
func (c *fakeClock) elapse() {
	timers := c.timers
	c.timers = nil
	for _, timer := range timers {
		if !timer.stopped {
			timer.stopped = true
			timer.f()
		}
	}
}

func newTestReporter(store *fakeReporterStore, now time.Time) (*WorkerReporter, *fakeCreator) {
	r, creator, _ := newClockedReporter(store, now)
	return r, creator
}

func newClockedReporter(store *fakeReporterStore, now time.Time) (*WorkerReporter, *fakeCreator, *fakeClock) {
	creator := &fakeCreator{}
	clock := &fakeClock{}
	r := NewWorkerReporter(WorkerReporterDeps{
		Store: store, Reports: creator, Now: func() time.Time { return now },
		AfterFunc: clock.AfterFunc, Spawn: func(f func()) { f() },
	})
	return r, creator, clock
}

func workerStore() *fakeReporterStore {
	return &fakeReporterStore{
		sessions: map[domain.SessionID]domain.SessionRecord{
			"w1": {ID: "w1", ProjectID: "p1", Kind: domain.KindWorker},
			"o1": {ID: "o1", ProjectID: "p1", Kind: domain.KindOrchestrator},
			"s1": {ID: "s1", Kind: domain.KindWorker},
		},
		conversation: map[domain.SessionID]string{},
		messages:     map[string][]domain.ConversationMessage{},
	}
}

func states(created []CreateInput) []domain.ReportState {
	out := make([]domain.ReportState, 0, len(created))
	for _, in := range created {
		out = append(out, in.State)
	}
	return out
}

func TestWorkerReporterObserve(t *testing.T) {
	type step struct {
		activity   domain.ActivityState
		terminated bool
		elapse     bool
	}
	active := step{activity: domain.ActivityActive}
	idle := step{activity: domain.ActivityIdle}
	wait := step{elapse: true}
	cases := []struct {
		name  string
		steps []step
		want  []domain.ReportState
	}{
		{"idle after work is done once quiet", []step{active, idle, wait}, []domain.ReportState{domain.ReportDone}},
		{"idle without quiet period writes nothing yet", []step{active, idle}, nil},
		{"waiting after work needs input", []step{active, {activity: domain.ActivityWaitingInput}}, []domain.ReportState{domain.ReportNeedsInput}},
		{"blocked after work needs input", []step{active, {activity: domain.ActivityBlocked}}, []domain.ReportState{domain.ReportNeedsInput}},
		{"exit after work is stuck", []step{active, {activity: domain.ActivityExited}}, []domain.ReportState{domain.ReportStuck}},
		{"idle never seen active is ignored", []step{idle, wait}, nil},
		{"termination is ignored", []step{active, {activity: domain.ActivityExited, terminated: true}, wait}, nil},
		{"termination cancels a pending done", []step{active, idle, {activity: domain.ActivityExited, terminated: true}, wait}, nil},
		{"idle gaps between steps give one done", []step{active, idle, active, idle, active, idle, wait}, []domain.ReportState{domain.ReportDone}},
		{"repeated idle gives one done", []step{active, idle, idle, wait, idle, wait}, []domain.ReportState{domain.ReportDone}},
		{"new work after a done gives another", []step{active, idle, wait, active, idle, wait}, []domain.ReportState{domain.ReportDone, domain.ReportDone}},
		{"question right after finishing replaces done", []step{active, idle, {activity: domain.ActivityWaitingInput}, wait}, []domain.ReportState{domain.ReportNeedsInput}},
		{"exit after finishing keeps done", []step{active, idle, {activity: domain.ActivityExited}, wait}, []domain.ReportState{domain.ReportDone}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, creator, clock := newClockedReporter(workerStore(), time.Now())
			for _, s := range tc.steps {
				if s.elapse {
					clock.elapse()
					continue
				}
				r.Observe(t.Context(), "w1", s.activity, s.terminated, time.Time{})
			}
			got := states(creator.created)
			if len(got) != len(tc.want) {
				t.Fatalf("reports = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("reports = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestWorkerReporterResumedTaskKeepsItsStart(t *testing.T) {
	start := time.Date(2026, 11, 24, 5, 0, 0, 0, time.UTC)
	now := start
	store := workerStore()
	store.conversation["w1"] = "c1"
	store.messages["c1"] = []domain.ConversationMessage{
		{Role: domain.MessageRoleAssistant, Text: "step one done", CreatedAt: start.Add(time.Second)},
		{Role: domain.MessageRoleAssistant, Text: "", CreatedAt: start.Add(2 * time.Second)},
	}
	creator := &fakeCreator{}
	clock := &fakeClock{}
	r := NewWorkerReporter(WorkerReporterDeps{
		Store: store, Reports: creator, Now: func() time.Time { return now },
		AfterFunc: clock.AfterFunc, Spawn: func(f func()) { f() },
	})
	r.Observe(t.Context(), "w1", domain.ActivityActive, false, time.Time{})
	now = start.Add(time.Minute)
	r.Observe(t.Context(), "w1", domain.ActivityIdle, false, time.Time{})
	r.Observe(t.Context(), "w1", domain.ActivityActive, false, time.Time{})
	r.Observe(t.Context(), "w1", domain.ActivityIdle, false, time.Time{})
	clock.elapse()
	if len(creator.created) != 1 || creator.created[0].Note != "step one done" {
		t.Fatalf("reports = %+v, want one done covering the whole task", creator.created)
	}
}

func TestWorkerReporterWriteUsesFinalAssistantMessage(t *testing.T) {
	since := time.Date(2026, 11, 24, 5, 0, 0, 0, time.UTC)
	store := workerStore()
	store.conversation["w1"] = "c1"
	store.messages["c1"] = []domain.ConversationMessage{
		{Role: domain.MessageRoleAssistant, Text: "old answer", CreatedAt: since.Add(-time.Minute)},
		{Role: domain.MessageRoleUser, Text: "do the thing", CreatedAt: since},
		{Role: domain.MessageRoleAssistant, Text: "  Opened PR 12; tests pass.  ", CreatedAt: since.Add(time.Minute)},
	}
	r, creator := newTestReporter(store, since)
	r.Write(t.Context(), "w1", domain.ReportDone, Stretch{Since: since, Until: since.Add(time.Hour)})
	if len(creator.created) != 1 {
		t.Fatalf("created %d reports, want 1", len(creator.created))
	}
	got := creator.created[0]
	if got.State != domain.ReportDone || got.Note != "Opened PR 12; tests pass." {
		t.Fatalf("report = %+v", got)
	}
	if err := domain.ValidateReportContent(got.State, got.Note, got.Message, got.Outputs); err != nil {
		t.Fatalf("report is invalid: %v", err)
	}
}

func TestWorkerReporterWriteFallsBackToHistoryPointer(t *testing.T) {
	r, creator := newTestReporter(workerStore(), time.Now())
	r.Write(t.Context(), "w1", domain.ReportDone, Stretch{Since: time.Now(), Until: time.Now().Add(time.Hour)})
	if len(creator.created) != 1 || !strings.Contains(creator.created[0].Note, "ao session history w1") {
		t.Fatalf("reports = %+v", creator.created)
	}
}

func TestWorkerReporterWriteTruncatesLongMessage(t *testing.T) {
	since := time.Now()
	store := workerStore()
	store.conversation["w1"] = "c1"
	store.messages["c1"] = []domain.ConversationMessage{{Role: domain.MessageRoleAssistant, Text: strings.Repeat("é", 5000), CreatedAt: since}}
	r, creator := newTestReporter(store, since)
	r.Write(t.Context(), "w1", domain.ReportDone, Stretch{Since: since, Until: since.Add(time.Hour)})
	if n := utf8.RuneCountInString(creator.created[0].Note); n != domain.MaxReportTextCharacters {
		t.Fatalf("note has %d runes, want %d", n, domain.MaxReportTextCharacters)
	}
}

func TestWorkerReporterWriteSkips(t *testing.T) {
	since := time.Now()
	cases := []struct {
		name    string
		session domain.SessionID
		reports []domain.ReportRecord
	}{
		{"orchestrator", "o1", nil},
		{"standalone worker", "s1", nil},
		{"unknown session", "missing", nil},
		{"agent already reported", "w1", []domain.ReportRecord{{SessionID: "w1", CreatedAt: since.Add(time.Second)}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := workerStore()
			store.reports = tc.reports
			r, creator := newTestReporter(store, since)
			r.Write(t.Context(), tc.session, domain.ReportDone, Stretch{Since: since, Until: since.Add(time.Hour)})
			if len(creator.created) != 0 {
				t.Fatalf("created %+v, want none", creator.created)
			}
		})
	}
}

func TestWorkerReporterOlderAgentReportDoesNotSuppress(t *testing.T) {
	since := time.Now()
	store := workerStore()
	store.reports = []domain.ReportRecord{{SessionID: "w1", CreatedAt: since.Add(-time.Minute)}}
	r, creator := newTestReporter(store, since)
	r.Write(t.Context(), "w1", domain.ReportNeedsInput, Stretch{Since: since, Until: since.Add(time.Hour)})
	if len(creator.created) != 1 || creator.created[0].State != domain.ReportNeedsInput {
		t.Fatalf("reports = %+v", creator.created)
	}
}

func TestWorkerReporterRealTimerWritesDone(t *testing.T) {
	done := make(chan CreateInput, 1)
	r := NewWorkerReporter(WorkerReporterDeps{
		Store: workerStore(), Reports: creatorFunc(func(in CreateInput) { done <- in }),
		QuietPeriod: 10 * time.Millisecond,
	})
	r.Observe(t.Context(), "w1", domain.ActivityActive, false, time.Time{})
	r.Observe(t.Context(), "w1", domain.ActivityIdle, false, time.Time{})
	select {
	case in := <-done:
		if in.State != domain.ReportDone {
			t.Fatalf("state = %q, want done", in.State)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no done report after the quiet period")
	}
}

type creatorFunc func(CreateInput)

func (f creatorFunc) Create(_ context.Context, in CreateInput) (domain.ReportRecord, error) {
	f(in)
	return domain.ReportRecord{}, nil
}

func TestWorkerReporterUsesEventTimeNotDeliveryTime(t *testing.T) {
	start := time.Date(2026, 11, 24, 5, 0, 0, 0, time.UTC)
	store := workerStore()
	store.conversation["w1"] = "c1"
	store.messages["c1"] = []domain.ConversationMessage{
		{Role: domain.MessageRoleAssistant, Text: "answer written before the active event arrived", CreatedAt: start.Add(time.Second)},
	}
	// The reporter's clock is well past the events, as when CDC delivers late.
	r, creator, clock := newClockedReporter(store, start.Add(time.Hour))
	r.Observe(t.Context(), "w1", domain.ActivityActive, false, start)
	r.Observe(t.Context(), "w1", domain.ActivityIdle, false, start.Add(2*time.Second))
	clock.elapse()
	if len(creator.created) != 1 || creator.created[0].Note != "answer written before the active event arrived" {
		t.Fatalf("reports = %+v", creator.created)
	}
}

func TestWorkerReporterIgnoresALaterTurnsAnswer(t *testing.T) {
	start := time.Date(2026, 11, 24, 5, 0, 0, 0, time.UTC)
	store := workerStore()
	store.conversation["w1"] = "c1"
	store.messages["c1"] = []domain.ConversationMessage{
		{Role: domain.MessageRoleAssistant, Text: "first task result", CreatedAt: start.Add(time.Second)},
		{Role: domain.MessageRoleAssistant, Text: "next turn's answer", CreatedAt: start.Add(time.Minute)},
	}
	r, creator := newTestReporter(store, start)
	r.Write(t.Context(), "w1", domain.ReportDone, Stretch{Since: start, Until: start.Add(2 * time.Second)})
	if len(creator.created) != 1 || creator.created[0].Note != "first task result" {
		t.Fatalf("reports = %+v", creator.created)
	}
}

func TestWorkerReporterOwnReportDoesNotSuppressTheNextStretch(t *testing.T) {
	start := time.Date(2026, 11, 24, 5, 0, 0, 0, time.UTC)
	store := workerStore()
	r, creator := newTestReporter(store, start)
	first := Stretch{Since: start, Until: start.Add(time.Second)}
	r.Write(t.Context(), "w1", domain.ReportNeedsInput, first)
	// The automatic report persisted late, inside the next stretch.
	store.reports = []domain.ReportRecord{{ID: "auto-1", SessionID: "w1", CreatedAt: start.Add(3 * time.Second)}}
	r.Write(t.Context(), "w1", domain.ReportDone, Stretch{Since: start.Add(2 * time.Second), Until: start.Add(time.Minute)})
	if got := states(creator.created); len(got) != 2 || got[1] != domain.ReportDone {
		t.Fatalf("reports = %v, want [needs_input done]", got)
	}
}

func TestWorkerReporterAgentReportOutsideStretchDoesNotSuppress(t *testing.T) {
	start := time.Date(2026, 11, 24, 5, 0, 0, 0, time.UTC)
	store := workerStore()
	store.reports = []domain.ReportRecord{{ID: "agent-1", SessionID: "w1", CreatedAt: start.Add(time.Hour)}}
	r, creator := newTestReporter(store, start)
	r.Write(t.Context(), "w1", domain.ReportDone, Stretch{Since: start, Until: start.Add(time.Minute)})
	if len(creator.created) != 1 {
		t.Fatalf("reports = %+v, want one", creator.created)
	}
}
