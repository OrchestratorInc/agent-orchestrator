package store_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func TestListActiveWorkerPreviews(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "previews")
	want := map[domain.SessionID]domain.SessionPreviewRecord{}
	for _, tc := range []struct {
		name       string
		kind       domain.SessionKind
		terminated bool
		standalone bool
		url        string
	}{
		{name: "blank", kind: domain.KindWorker},
		{name: "selected file", kind: domain.KindWorker, url: "docs/report.html"},
		{name: "external", kind: domain.KindWorker, url: "https://example.test/preview"},
		{name: "standalone", kind: domain.KindWorker, standalone: true, url: "index.html"},
		{name: "terminated", kind: domain.KindWorker, terminated: true, url: "index.html"},
		{name: "orchestrator", kind: domain.KindOrchestrator, url: "index.html"},
	} {
		rec := sampleRecord("previews")
		rec.Kind, rec.IsTerminated = tc.kind, tc.terminated
		if tc.standalone {
			rec.ProjectID = ""
		}
		rec.Metadata.WorkspacePath = "/workspace/" + tc.name
		rec.Metadata.Prompt = strings.Repeat("unrelated prompt ", 1024)
		created, err := s.CreateSession(ctx, rec)
		if err != nil {
			t.Fatalf("create %s: %v", tc.name, err)
		}
		// A blank preview with a nonzero revision must remain a candidate so a
		// file cleared by the poller can be restored when it reappears.
		if ok, err := s.SetSessionPreviewURL(ctx, created.ID, tc.url, time.Now()); err != nil || !ok {
			t.Fatalf("set preview %s: ok=%v err=%v", tc.name, ok, err)
		}
		if tc.kind == domain.KindWorker && !tc.terminated {
			want[created.ID] = domain.SessionPreviewRecord{
				ID: created.ID, WorkspacePath: rec.Metadata.WorkspacePath,
				PreviewURL: tc.url, PreviewRevision: 1,
			}
		}
	}

	rows, err := s.ListActiveWorkerPreviews(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[domain.SessionID]domain.SessionPreviewRecord, len(rows))
	for _, row := range rows {
		got[row.ID] = row
	}
	if len(rows) != len(want) || !reflect.DeepEqual(got, want) {
		t.Fatalf("preview rows = %#v, want %#v", rows, want)
	}
}

func TestListActiveWorkerPreviewsEmptyAndCancelled(t *testing.T) {
	s := newTestStore(t)
	rows, err := s.ListActiveWorkerPreviews(context.Background())
	if err != nil || len(rows) != 0 {
		t.Fatalf("empty store: rows=%v err=%v", rows, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.ListActiveWorkerPreviews(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read: %v", err)
	}
}

// Compare the old polling read with its replacement using synthetic history.
// No live daemon data is needed: only 10 of 1,000 workers remain active.
func BenchmarkSessionPreviewRead(b *testing.B) {
	s := sqlitetest.MustOpen(b)
	ctx := context.Background()
	if err := s.UpsertProject(ctx, domain.ProjectRecord{
		ID: "previews", Path: b.TempDir(), RegisteredAt: time.Now(),
	}); err != nil {
		b.Fatal(err)
	}
	for i := range 1000 {
		rec := sampleRecord("previews")
		rec.IsTerminated = i >= 10
		rec.Metadata.Prompt = strings.Repeat("p", 4096)
		if _, err := s.CreateSession(ctx, rec); err != nil {
			b.Fatal(err)
		}
	}
	b.Run("AllSessions", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := s.ListAllSessions(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("ActiveWorkerPreviews", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := s.ListActiveWorkerPreviews(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
}
