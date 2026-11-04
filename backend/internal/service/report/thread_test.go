package report

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type fakeThreadStore struct {
	sessions     []domain.SessionRecord
	worker       domain.SessionRecord
	conversation domain.ConversationRecord
	convoErr     error
	upserts      []domain.ConversationActivity
	upsertErr    error
}

func (f *fakeThreadStore) ListSessions(context.Context, domain.ProjectID) ([]domain.SessionRecord, error) {
	return f.sessions, nil
}

func (f *fakeThreadStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	if f.worker.ID == id {
		return f.worker, true, nil
	}
	return domain.SessionRecord{}, false, nil
}

func (f *fakeThreadStore) ConversationForSession(context.Context, domain.SessionID) (domain.ConversationRecord, error) {
	return f.conversation, f.convoErr
}

func (f *fakeThreadStore) UpsertActivity(_ context.Context, _ string, _ string, activity domain.ConversationActivity, _ time.Time) error {
	if f.upsertErr != nil {
		return f.upsertErr
	}
	f.upserts = append(f.upserts, activity)
	return nil
}

func threadStoreWith() *fakeThreadStore {
	return &fakeThreadStore{
		sessions: []domain.SessionRecord{
			{ID: "orch-1", ProjectID: "ao", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat, CreatedAt: time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)},
		},
		worker:       domain.SessionRecord{ID: "ao-7", ProjectID: "ao", DisplayName: "Builder"},
		conversation: domain.ConversationRecord{ID: "conv-1"},
	}
}

func TestCreateShowsTheReportInTheOrchestratorThreadAtIngestionTime(t *testing.T) {
	now := time.Date(2026, 11, 4, 10, 52, 0, 0, time.UTC)
	st := threadStoreWith()
	base := &fakeStore{ok: true, session: domain.SessionRecord{ID: "ao-7", ProjectID: "ao", Kind: domain.KindWorker}}
	recorder := NewConversationThread(st)
	s := New(Deps{Store: base, Threads: recorder, Now: func() time.Time { return now }, NewID: func() string { return "rpt_1" }})
	created, err := s.Create(context.Background(), CreateInput{
		SessionID: "ao-7", State: domain.ReportDone, Note: "finished",
		Outputs: []domain.ReportOutput{
			{Kind: domain.ReportOutputArtifact, Reference: "report.html"},
			{Kind: domain.ReportOutputPRCreated, Reference: "https://github.com/o/r/pull/1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.upserts) != 1 {
		t.Fatalf("upserts = %d, want the one report row at report time", len(st.upserts))
	}
	activity := st.upserts[0]
	if activity.Kind != domain.ActivityKindAOAction || activity.Status != domain.ActivityStatusCompleted {
		t.Fatalf("activity kind/status = %s/%s", activity.Kind, activity.Status)
	}
	if activity.ID != "ao-action:worker.report:rpt_1" || activity.ProviderItemID != activity.ID {
		t.Fatalf("activity id = %q, providerItemID = %q", activity.ID, activity.ProviderItemID)
	}
	var detail struct {
		Action          string `json:"action"`
		OperationID     string `json:"operationId"`
		TargetSessionID string `json:"targetSessionId"`
		ProjectID       string `json:"projectId"`
		DisplayName     string `json:"displayName"`
		Href            string `json:"href"`
		State           string `json:"state"`
		Text            string `json:"text"`
		Outputs         []struct {
			Kind      string `json:"kind"`
			Reference string `json:"reference"`
		} `json:"outputs"`
	}
	if err := json.Unmarshal(activity.Detail, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Action != "worker.report" || detail.OperationID != created.ID ||
		detail.TargetSessionID != "ao-7" || detail.ProjectID != "ao" ||
		detail.DisplayName != "Builder" || detail.Href != "ao://sessions/ao/ao-7" ||
		detail.State != "done" || detail.Text != "finished" ||
		len(detail.Outputs) != 2 || detail.Outputs[0].Reference != "report.html" ||
		detail.Outputs[1].Reference != "https://github.com/o/r/pull/1" {
		t.Fatalf("detail = %+v", detail)
	}
}

func TestThreadRowPrefersTheNoteAndNamesFreeFormReportsInformation(t *testing.T) {
	now := time.Date(2026, 11, 4, 10, 52, 0, 0, time.UTC)
	st := threadStoreWith()
	base := &fakeStore{ok: true, session: domain.SessionRecord{ID: "ao-7", ProjectID: "ao", Kind: domain.KindWorker}}
	s := New(Deps{Store: base, Threads: NewConversationThread(st), Now: func() time.Time { return now }, NewID: func() string { return "rpt_2" }})
	if _, err := s.Create(context.Background(), CreateInput{SessionID: "ao-7", Message: "free form"}); err != nil {
		t.Fatal(err)
	}
	var detail struct {
		State string `json:"state"`
		Text  string `json:"text"`
	}
	if len(st.upserts) != 1 {
		t.Fatalf("upserts = %d", len(st.upserts))
	}
	if err := json.Unmarshal(st.upserts[0].Detail, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.State != "information" || detail.Text != "free form" {
		t.Fatalf("detail state/text = %q/%q, want information/free form", detail.State, detail.Text)
	}
}

func TestThreadRowSkipsWhenNoChatOrchestratorCanShowIt(t *testing.T) {
	now := time.Date(2026, 11, 4, 10, 52, 0, 0, time.UTC)
	tests := []struct {
		name     string
		sessions []domain.SessionRecord
	}{
		{"no orchestrator", nil},
		{"terminated", []domain.SessionRecord{{ID: "orch-1", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat, IsTerminated: true}}},
		{"terminal orchestrator has no chat thread", []domain.SessionRecord{{ID: "orch-1", Kind: domain.KindOrchestrator, Mode: domain.SessionModeTUI}}},
		{"worker only", []domain.SessionRecord{{ID: "ao-7", Kind: domain.KindWorker, Mode: domain.SessionModeChat}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := threadStoreWith()
			st.sessions = tc.sessions
			base := &fakeStore{ok: true, session: domain.SessionRecord{ID: "ao-7", ProjectID: "ao", Kind: domain.KindWorker}}
			s := New(Deps{Store: base, Threads: NewConversationThread(st), Now: func() time.Time { return now }})
			if _, err := s.Create(context.Background(), CreateInput{SessionID: "ao-7", State: domain.ReportDone, Note: "finished"}); err != nil {
				t.Fatal(err)
			}
			if len(st.upserts) != 0 {
				t.Fatalf("upserts = %d, want none", len(st.upserts))
			}
		})
	}
}

// An exited agent does not close the thread: the chat stays openable and the
// next turn still receives the injected batch, so the row still lands.
func TestThreadRowStillShowsForAnExitedChatOrchestrator(t *testing.T) {
	now := time.Date(2026, 11, 4, 10, 52, 0, 0, time.UTC)
	st := threadStoreWith()
	st.sessions = []domain.SessionRecord{
		{ID: "orch-1", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityExited}},
	}
	base := &fakeStore{ok: true, session: domain.SessionRecord{ID: "ao-7", ProjectID: "ao", Kind: domain.KindWorker}}
	s := New(Deps{Store: base, Threads: NewConversationThread(st), Now: func() time.Time { return now }})
	if _, err := s.Create(context.Background(), CreateInput{SessionID: "ao-7", State: domain.ReportDone, Note: "finished"}); err != nil {
		t.Fatal(err)
	}
	if len(st.upserts) != 1 {
		t.Fatalf("upserts = %d, want the row for the exited-but-open thread", len(st.upserts))
	}
}

func TestThreadRowIsBestEffort(t *testing.T) {
	now := time.Date(2026, 11, 4, 10, 52, 0, 0, time.UTC)
	base := &fakeStore{ok: true, session: domain.SessionRecord{ID: "ao-7", ProjectID: "ao", Kind: domain.KindWorker}}
	t.Run("missing conversation", func(t *testing.T) {
		st := threadStoreWith()
		st.convoErr = domain.ErrNoConversation
		s := New(Deps{Store: base, Threads: NewConversationThread(st), Now: func() time.Time { return now }})
		if _, err := s.Create(context.Background(), CreateInput{SessionID: "ao-7", State: domain.ReportDone, Note: "finished"}); err != nil {
			t.Fatalf("report failed: %v", err)
		}
	})
	t.Run("store write failure", func(t *testing.T) {
		st := threadStoreWith()
		st.upsertErr = errors.New("disk on fire")
		s := New(Deps{Store: base, Threads: NewConversationThread(st), Now: func() time.Time { return now }})
		created, err := s.Create(context.Background(), CreateInput{SessionID: "ao-7", State: domain.ReportDone, Note: "finished"})
		if err != nil || created.ID == "" {
			t.Fatalf("report failed: %v", err)
		}
	})
	t.Run("recorder is optional", func(t *testing.T) {
		s := New(Deps{Store: base, Now: func() time.Time { return now }})
		if _, err := s.Create(context.Background(), CreateInput{SessionID: "ao-7", State: domain.ReportDone, Note: "finished"}); err != nil {
			t.Fatalf("report failed: %v", err)
		}
	})
}

func TestThreadRowPrefersTheNewestActiveChatOrchestrator(t *testing.T) {
	now := time.Date(2026, 11, 4, 10, 52, 0, 0, time.UTC)
	st := threadStoreWith()
	st.sessions = []domain.SessionRecord{
		{ID: "orch-old", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat, CreatedAt: now.Add(-2 * time.Hour)},
		{ID: "orch-dead", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat, IsTerminated: true, CreatedAt: now.Add(-time.Hour)},
		{ID: "orch-new", Kind: domain.KindOrchestrator, Mode: domain.SessionModeChat, CreatedAt: now.Add(-30 * time.Minute)},
	}
	base := &fakeStore{ok: true, session: domain.SessionRecord{ID: "ao-7", ProjectID: "ao", Kind: domain.KindWorker}}
	s := New(Deps{Store: base, Threads: NewConversationThread(st), Now: func() time.Time { return now }})
	if _, err := s.Create(context.Background(), CreateInput{SessionID: "ao-7", State: domain.ReportDone, Note: "finished"}); err != nil {
		t.Fatal(err)
	}
	if len(st.upserts) != 1 {
		t.Fatalf("upserts = %d", len(st.upserts))
	}
}
