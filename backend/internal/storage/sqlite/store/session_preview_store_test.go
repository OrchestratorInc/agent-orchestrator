package store_test

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestListPreviewSessionsReturnsLiveWorkersOnly(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "mer")
	var want domain.SessionRecord
	for i, change := range []func(*domain.SessionRecord){
		func(*domain.SessionRecord) {},
		func(r *domain.SessionRecord) { r.Kind = domain.KindOrchestrator },
		func(r *domain.SessionRecord) { r.IsTerminated = true },
	} {
		rec := sampleRecord("mer")
		rec.Metadata.PreviewURL = "http://127.0.0.1:3001/preview/index.html"
		rec.Metadata.PreviewRevision = 3
		change(&rec)
		created, err := s.CreateSession(ctx, rec)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			want = created
		}
	}
	got, err := s.ListPreviewSessions(ctx)
	if err != nil || len(got) != 1 {
		t.Fatalf("ListPreviewSessions = %v, %v; want one session", got, err)
	}
	g := got[0]
	if g.ID != want.ID || g.Kind != domain.KindWorker || g.Metadata.WorkspacePath != "/ws" ||
		g.Metadata.PreviewURL != want.Metadata.PreviewURL || g.Metadata.PreviewRevision != 3 {
		t.Fatalf("ListPreviewSessions = %+v, want %s with its preview fields", g, want.ID)
	}
}
