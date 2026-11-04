package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/report"
)

// Ingesting a worker report must leave a report row in the active Chat
// orchestrator's conversation projection at report time, through the same
// trigger-covered activity write every other live timeline row uses — not as
// a later delivery- or piggyback-time backfill.
func TestReportIngestionLandsALiveThreadRowInTheChatConversation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "rtx")

	orch := sampleRecord("rtx")
	orch.Kind = domain.KindOrchestrator
	orch.Mode = domain.SessionModeChat
	orchestrator, err := s.CreateSession(ctx, orch)
	if err != nil {
		t.Fatalf("create orchestrator: %v", err)
	}
	conversation, err := s.CreateConversation(ctx, "conv-orch", domain.ConversationScopeSession, "rtx", orchestrator.ID, histClock)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	conversationID := conversation.ID
	workerRec := sampleRecord("rtx")
	workerRec.DisplayName = "Builder"
	worker, err := s.CreateSession(ctx, workerRec)
	if err != nil {
		t.Fatalf("create worker: %v", err)
	}

	before, err := s.LatestSeq(ctx)
	if err != nil {
		t.Fatal(err)
	}
	svc := report.New(report.Deps{
		Store:   s,
		Threads: report.NewConversationThread(s),
		Now:     func() time.Time { return histClock.Add(time.Minute) },
	})
	created, err := svc.Create(ctx, report.CreateInput{
		SessionID: worker.ID, State: domain.ReportDone, Note: "finished",
		Outputs: []domain.ReportOutput{{Kind: domain.ReportOutputArtifact, Reference: "report.html"}},
	})
	if err != nil {
		t.Fatalf("create report: %v", err)
	}

	snapshot, err := s.LoadConversationSnapshot(ctx, conversationID)
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	var found *domain.ConversationActivity
	for i, item := range snapshot.Activities {
		if item.ProviderItemID == report.ThreadActivityID(created.ID) {
			found = &snapshot.Activities[i]
		}
	}
	if found == nil {
		t.Fatalf("no thread row for %s in %+v", created.ID, snapshot.Activities)
	}
	if found.Kind != domain.ActivityKindAOAction || found.Status != domain.ActivityStatusCompleted {
		t.Fatalf("thread row kind/status = %s/%s", found.Kind, found.Status)
	}
	var detail struct {
		Action          string `json:"action"`
		TargetSessionID string `json:"targetSessionId"`
		DisplayName     string `json:"displayName"`
		State           string `json:"state"`
		Text            string `json:"text"`
	}
	if err := json.Unmarshal(found.Detail, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Action != "worker.report" || detail.TargetSessionID != string(worker.ID) ||
		detail.DisplayName != "Builder" || detail.State != "done" || detail.Text != "finished" {
		t.Fatalf("thread row detail = %+v", detail)
	}

	events, err := s.EventsAfter(ctx, before, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 {
		t.Fatal("no change_log event after report ingestion; the open thread would never be invalidated")
	}
}
