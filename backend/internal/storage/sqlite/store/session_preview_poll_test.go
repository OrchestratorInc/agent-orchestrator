package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// The preview poller runs every 250 ms, so its query returns only live
// workers, with the five fields the poller reads.
func TestListPreviewPollSessionsReadsLiveWorkersOnly(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "mer")
	create := func(change func(*domain.SessionRecord)) domain.SessionRecord {
		t.Helper()
		rec := sampleRecord("mer")
		change(&rec)
		created, err := s.CreateSession(ctx, rec)
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	worker := create(func(r *domain.SessionRecord) { r.Metadata.WorkspacePath = "/ws/worker" })
	create(func(r *domain.SessionRecord) { r.Kind = domain.KindOrchestrator })
	create(func(r *domain.SessionRecord) { r.IsTerminated = true })
	const previewURL = "http://127.0.0.1:3000/"
	if ok, err := s.SetSessionPreviewURL(ctx, worker.ID, previewURL, time.Now().UTC()); err != nil || !ok {
		t.Fatalf("set preview = %v, %v", ok, err)
	}

	got, err := s.ListPreviewPollSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != worker.ID {
		t.Fatalf("sessions = %+v, want only live worker %s", got, worker.ID)
	}
	meta := got[0].Metadata
	if got[0].Kind != domain.KindWorker || meta.WorkspacePath != "/ws/worker" || meta.PreviewURL != previewURL || meta.PreviewRevision != worker.Metadata.PreviewRevision+1 {
		t.Fatalf("worker = kind %q, workspace %q, preview %q rev %d; want worker, /ws/worker, %q rev %d",
			got[0].Kind, meta.WorkspacePath, meta.PreviewURL, meta.PreviewRevision, previewURL, worker.Metadata.PreviewRevision+1)
	}
}
