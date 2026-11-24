package store_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
)

// A kept record is listed only once its session row is gone, keeps the
// session's artifact rows, and drops its project once the project is removed.
func TestKeptArtifactsOutliveTheirSession(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "kep")
	sess := terminalSession(t, s, "kep", 0)
	other := terminalSession(t, s, "kep", 0)
	now := time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	if _, _, err := s.AddSessionArtifacts(ctx, sess.ID, []domain.SessionArtifactRegistration{{Path: "report.html"}}, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AddSessionArtifacts(ctx, other.ID, []domain.SessionArtifactRegistration{{Path: "other.md"}}, now); err != nil {
		t.Fatal(err)
	}
	before, err := s.ListSessionArtifacts(ctx, sess.ID)
	if err != nil || len(before) != 1 {
		t.Fatalf("session artifacts = %v, %v", before, err)
	}

	kept := domain.KeptArtifactSession{SessionID: sess.ID, ProjectID: "kep", Title: "Quarterly report", KeptAt: now}
	if err := s.KeepSessionArtifacts(ctx, kept); err != nil {
		t.Fatal(err)
	}
	// Kept again by a retried delete: still one record, one row.
	if err := s.KeepSessionArtifacts(ctx, kept); err != nil {
		t.Fatal(err)
	}
	if list, err := s.ListKeptArtifactSessions(ctx); err != nil || len(list) != 0 {
		t.Fatalf("listed while the session exists: %v, %v", list, err)
	}
	if _, found, err := s.GetKeptArtifactSession(ctx, sess.ID); err != nil || found {
		t.Fatalf("found while the session exists: %v, %v", found, err)
	}

	if deleted, err := s.DeleteTerminatedSession(ctx, sess.ID); err != nil || !deleted {
		t.Fatalf("delete = %v, %v", deleted, err)
	}
	list, err := s.ListKeptArtifactSessions(ctx)
	if err != nil || len(list) != 1 || list[0].SessionID != sess.ID || list[0].ProjectID != "kep" || list[0].Title != "Quarterly report" || !list[0].KeptAt.Equal(now) {
		t.Fatalf("kept list = %+v, %v", list, err)
	}
	rows, err := s.ListKeptArtifacts(ctx, sess.ID)
	if err != nil || len(rows) != 1 || rows[0].ID != before[0].ID || rows[0].Path != "report.html" || rows[0].RegisteredAt == nil {
		t.Fatalf("kept rows = %+v, %v; want the session's registered row", rows, err)
	}

	if ok, err := s.ArchiveProject(ctx, "kep", now); err != nil || !ok {
		t.Fatalf("archive project = %v, %v", ok, err)
	}
	got, found, err := s.GetKeptArtifactSession(ctx, sess.ID)
	if err != nil || !found || got.ProjectID != "" {
		t.Fatalf("after project removal = %+v, %v, %v; want no project", got, found, err)
	}
	removed, err := s.ListRemovedProjectSessionIDs(ctx)
	if err != nil || !slices.Equal(removed, []domain.SessionID{other.ID}) {
		t.Fatalf("removed project sessions = %v, %v; want %s", removed, err, other.ID)
	}

	if err := s.RemoveKeptArtifact(ctx, sess.ID, "report.html"); err != nil {
		t.Fatal(err)
	}
	if rows, err := s.ListKeptArtifacts(ctx, sess.ID); err != nil || len(rows) != 0 {
		t.Fatalf("rows after remove = %v, %v", rows, err)
	}
	if err := s.ForgetKeptArtifactSession(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	if list, err := s.ListKeptArtifactSessions(ctx); err != nil || len(list) != 0 {
		t.Fatalf("listed after forget: %v, %v", list, err)
	}
}
