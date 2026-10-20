package store_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func TestListPreviewPollSessions(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	s := sqlitetest.MustOpenAt(t, dataDir)
	seedProject(t, s, "alpha")
	seedProject(t, s, "beta")

	var created []domain.PreviewPollSession
	for _, tc := range []struct {
		project    string
		kind       domain.SessionKind
		terminated bool
		url        string
		revision   int64
	}{
		{"beta", domain.KindWorker, false, "docs/index.html", 5},
		{"alpha", domain.KindWorker, false, "", 2},
		{"alpha", domain.KindWorker, true, "archived.html", 3},
		{"alpha", domain.KindOrchestrator, false, "orchestrator.html", 4},
		{"alpha", domain.KindWorker, false, "https://example.test/preview", 7},
	} {
		rec := sampleRecord(tc.project)
		rec.Kind, rec.IsTerminated = tc.kind, tc.terminated
		rec.Metadata.PreviewURL, rec.Metadata.PreviewRevision = tc.url, tc.revision
		rec.Metadata.Prompt = strings.Repeat("unused prompt ", 1000)
		row, err := s.CreateSession(ctx, rec)
		if err != nil {
			t.Fatal(err)
		}
		created = append(created, domain.PreviewPollSession{
			ID: row.ID, WorkspacePath: row.Metadata.WorkspacePath,
			PreviewURL: tc.url, PreviewRevision: tc.revision,
		})
	}
	got, err := s.ListPreviewPollSessions(ctx)
	want := []domain.PreviewPollSession{created[1], created[4], created[0]}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("preview sessions = %+v, %v; want %+v", got, err, want)
	}
	all, err := s.ListAllSessions(ctx)
	if err != nil || len(all) != len(created) {
		t.Fatalf("full session list = %d rows, %v; want %d", len(all), err, len(created))
	}

	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "ao.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	rows, err := raw.QueryContext(ctx, `EXPLAIN QUERY PLAN
SELECT id, workspace_path, preview_url, preview_revision
FROM sessions WHERE is_terminated = 0 AND kind = 'worker'
ORDER BY project_id, num`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var plans []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plans = append(plans, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(plans, "\n"), "idx_sessions_preview_poll") {
		t.Fatalf("preview query did not use the partial index: %v", plans)
	}
}

func BenchmarkPreviewPollSessionQueries(b *testing.B) {
	ctx := context.Background()
	dataDir := b.TempDir()
	s := sqlitetest.MustOpenAt(b, dataDir)
	now := time.Now().UTC()
	if err := s.UpsertProject(ctx, domain.ProjectRecord{ID: "bench", Path: "/tmp/preview-bench", RegisteredAt: now}); err != nil {
		b.Fatal(err)
	}
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "ao.db"))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	tx, err := raw.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO sessions
(id, project_id, num, kind, harness, activity_state, activity_last_at,
is_terminated, branch, workspace_path, prompt, latest_user_prompt,
latest_assistant_update, created_at, updated_at)
VALUES (?, 'bench', ?, ?, 'fake', 'idle', ?, ?, '', '/tmp/preview-bench', ?, ?, ?, ?, ?)`)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = stmt.Close() }()
	prompt, user, assistant := strings.Repeat("p", 2048), strings.Repeat("u", 512), strings.Repeat("a", 128)
	const total = 2249
	for i := range total {
		kind := domain.KindWorker
		if i >= 15 && i < 28 {
			kind = domain.KindOrchestrator
		}
		if _, err := stmt.ExecContext(ctx, fmt.Sprintf("bench-%04d", i), i+1, kind, now, i >= 28, prompt, user, assistant, now, now); err != nil {
			b.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}

	b.Run("full_session_list", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			rows, err := s.ListAllSessions(ctx)
			if err != nil || len(rows) != total {
				b.Fatalf("full list = %d rows, %v", len(rows), err)
			}
		}
	})
	b.Run("active_worker_preview", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			rows, err := s.ListPreviewPollSessions(ctx)
			if err != nil || len(rows) != 15 {
				b.Fatalf("preview list = %d rows, %v", len(rows), err)
			}
		}
	})
}
