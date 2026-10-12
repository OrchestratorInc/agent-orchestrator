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
}
