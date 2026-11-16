package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestSessionArtifactVersionStoreAppendsListsAndPrunes(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "ao")
	sess, err := s.CreateSession(ctx, sampleRecord("ao"))
	if err != nil {
		t.Fatal(err)
	}
	mtime := time.Unix(0, 1_700_000_000_123_456_789).UTC()
	appendVersion := func(path, sha string, keep int) (domain.SessionArtifactVersion, []string) {
		t.Helper()
		v, pruned, err := s.AppendSessionArtifactVersion(ctx, domain.SessionArtifactVersion{
			SessionID: sess.ID, Path: path, SHA256: sha, Size: 3, ContentType: "text/html; charset=utf-8",
			ModTime: mtime, CreatedAt: mtime, Version: 99,
		}, keep)
		if err != nil {
			t.Fatal(err)
		}
		return v, pruned
	}

	if v, pruned := appendVersion("page.html", "aaa", 3); v.Version != 1 || len(pruned) != 0 || !v.ModTime.Equal(mtime) {
		t.Fatalf("first = %+v pruned=%v", v, pruned)
	}
	appendVersion("page.html", "bbb", 3)
	appendVersion("page.html", "aaa", 3)
	appendVersion("notes.md", "ccc", 3)
	v, pruned := appendVersion("page.html", "ddd", 3)
	if v.Version != 4 || len(pruned) != 1 || pruned[0] != "aaa" {
		t.Fatalf("fourth = %+v pruned=%v, want version 4 pruning v1 (aaa)", v, pruned)
	}

	all, err := s.ListSessionArtifactVersions(ctx, sess.ID, "page.html")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].Version != 4 || all[2].Version != 2 {
		t.Fatalf("versions = %+v, want 4,3,2", all)
	}
	latest, err := s.ListLatestSessionArtifactVersions(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(latest) != 2 || latest[0].Path != "notes.md" || latest[0].Version != 1 || latest[1].Path != "page.html" || latest[1].SHA256 != "ddd" {
		t.Fatalf("latest = %+v", latest)
	}
	// v3 still references aaa after v1 was pruned.
	if n, err := s.CountSessionArtifactVersionsBySHA(ctx, sess.ID, "aaa"); err != nil || n != 1 {
		t.Fatalf("count aaa = %d err=%v, want 1", n, err)
	}
	if n, err := s.CountSessionArtifactVersionsBySHA(ctx, "other", "ddd"); err != nil || n != 0 {
		t.Fatalf("count for other session = %d err=%v, want 0", n, err)
	}

	if err := s.DeleteSessionArtifactVersions(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	if latest, err := s.ListLatestSessionArtifactVersions(ctx, sess.ID); err != nil || len(latest) != 0 {
		t.Fatalf("latest after delete = %+v err=%v, want none", latest, err)
	}
}

func TestSessionArtifactVersionStoreKeepsSourceAndHarness(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "ao")
	sess, err := s.CreateSession(ctx, sampleRecord("ao"))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(0, 1_700_000_000_000_000_000).UTC()
	var restoredFrom int64
	add := func(sha string, source domain.ArtifactVersionSource, harness domain.AgentHarness) {
		t.Helper()
		_, _, err := s.AppendSessionArtifactVersion(ctx, domain.SessionArtifactVersion{
			SessionID: sess.ID, Path: "plan.md", SHA256: sha, Size: 1, ContentType: "text/markdown", ModTime: at, CreatedAt: at,
			Source: source, Harness: harness, RestoredFrom: restoredFrom,
		}, 0)
		if err != nil {
			t.Fatal(err)
		}
	}
	add("aaa", domain.ArtifactVersionSourceUser, domain.HarnessClaudeCode)
	restoredFrom = 1
	add("bbb", "", "") // an unset source is the agent's, with no harness recorded

	got, err := s.ListSessionArtifactVersions(ctx, sess.ID, "plan.md")
	if err != nil || len(got) != 2 {
		t.Fatalf("versions = %+v err=%v", got, err)
	}
	if got[0].Source != domain.ArtifactVersionSourceAgent || got[0].Harness != "" {
		t.Errorf("second = source %q harness %q, want agent and none", got[0].Source, got[0].Harness)
	}
	if got[0].RestoredFrom != 1 || got[1].RestoredFrom != 0 {
		t.Errorf("restoredFrom = %d and %d, want 1 and 0", got[0].RestoredFrom, got[1].RestoredFrom)
	}
	if got[1].Source != domain.ArtifactVersionSourceUser || got[1].Harness != domain.HarnessClaudeCode {
		t.Errorf("first = source %q harness %q, want user and claude-code", got[1].Source, got[1].Harness)
	}
}
