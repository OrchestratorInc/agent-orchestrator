package report

import (
	"context"
	"errors"
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
	return domain.ReportRecord{}, nil
}

func newTestReporter(store *fakeReporterStore, now time.Time) (*WorkerReporter, *fakeCreator) {
	creator := &fakeCreator{}
	return NewWorkerReporter(WorkerReporterDeps{Store: store, Reports: creator, Now: func() time.Time { return now }}), creator
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

func TestWorkerReporterTransitions(t *testing.T) {
	now := time.Date(2026, 11, 24, 5, 0, 0, 0, time.UTC)
	cases := []struct {
		name       string
		steps      []domain.ActivityState
		terminated bool
		want       domain.ReportState
	}{
		{"idle after work is done", []domain.ActivityState{domain.ActivityActive, domain.ActivityIdle}, false, domain.ReportDone},
		{"waiting after work needs input", []domain.ActivityState{domain.ActivityActive, domain.ActivityWaitingInput}, false, domain.ReportNeedsInput},
		{"blocked after work needs input", []domain.ActivityState{domain.ActivityActive, domain.ActivityBlocked}, false, domain.ReportNeedsInput},
		{"exit after work is stuck", []domain.ActivityState{domain.ActivityActive, domain.ActivityExited}, false, domain.ReportStuck},
		{"idle never seen active is ignored", []domain.ActivityState{domain.ActivityIdle}, false, ""},
		{"termination is ignored", []domain.ActivityState{domain.ActivityActive, domain.ActivityExited}, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := newTestReporter(workerStore(), now)
			var got domain.ReportState
			for i, step := range tc.steps {
				last := i == len(tc.steps)-1
				got, _ = r.Transition("w1", step, last && tc.terminated)
			}
			if got != tc.want {
				t.Fatalf("Transition = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWorkerReporterReportsOnceThenRearms(t *testing.T) {
	r, _ := newTestReporter(workerStore(), time.Now())
	r.Transition("w1", domain.ActivityActive, false)
	if got, _ := r.Transition("w1", domain.ActivityIdle, false); got != domain.ReportDone {
		t.Fatalf("first idle = %q, want done", got)
	}
	if got, _ := r.Transition("w1", domain.ActivityIdle, false); got != "" {
		t.Fatalf("repeated idle = %q, want none", got)
	}
	r.Transition("w1", domain.ActivityActive, false)
	if got, _ := r.Transition("w1", domain.ActivityIdle, false); got != domain.ReportDone {
		t.Fatalf("idle after new work = %q, want done", got)
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
	r.Write(t.Context(), "w1", domain.ReportDone, since)
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
	r.Write(t.Context(), "w1", domain.ReportDone, time.Now())
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
	r.Write(t.Context(), "w1", domain.ReportDone, since)
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
			r.Write(t.Context(), tc.session, domain.ReportDone, since)
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
	r.Write(t.Context(), "w1", domain.ReportNeedsInput, since)
	if len(creator.created) != 1 || creator.created[0].State != domain.ReportNeedsInput {
		t.Fatalf("reports = %+v", creator.created)
	}
}
