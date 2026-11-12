package store_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func prLink(t *testing.T, id domain.SessionID, raw string, relation domain.SessionPRRelation) domain.SessionPRLink {
	t.Helper()
	ref, err := domain.ParseChangeRequestURL(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", raw, err)
	}
	return domain.SessionPRLink{SessionID: id, Ref: ref, Relation: relation, CreatedAt: time.Now().UTC().Truncate(time.Second)}
}

func TestSessionPRLinksSurviveRestartAndOnlyExplicitReplaces(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	seedProject(t, s, "ao")
	sess, err := s.CreateSession(ctx, sampleRecord("ao"))
	if err != nil {
		t.Fatal(err)
	}
	link := prLink(t, sess.ID, "https://github.com/acme/app/pull/8020", domain.SessionPRReviewing)
	link.Title, link.State, link.Author = "Fix it", domain.PRStateOpen, "alice"
	if saved, err := s.SaveSessionPRLink(ctx, link, false); err != nil || !saved {
		t.Fatalf("first save saved=%v err=%v", saved, err)
	}
	// An explicit link sets the relation.
	link.Relation = domain.SessionPRContributing
	if saved, err := s.SaveSessionPRLink(ctx, link, true); err != nil || !saved {
		t.Fatalf("explicit save saved=%v err=%v", saved, err)
	}
	// An automatic signal leaves an existing link alone.
	link.Relation = domain.SessionPRReviewing
	if saved, err := s.SaveSessionPRLink(ctx, link, false); err != nil || saved {
		t.Fatalf("automatic save saved=%v err=%v, want a no-op", saved, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	links, err := s.ListSessionPRLinksForSessions(ctx, []domain.SessionID{sess.ID, "missing"})
	if err != nil || len(links[sess.ID]) != 1 || len(links["missing"]) != 0 {
		t.Fatalf("links after restart = %+v err=%v", links, err)
	}
	got := links[sess.ID][0]
	if got.Ref != link.Ref || got.Relation != domain.SessionPRContributing || got.Title != "Fix it" || got.State != domain.PRStateOpen || got.Author != "alice" {
		t.Fatalf("link after restart = %+v", got)
	}

	if removed, err := s.DeleteSessionPRLink(ctx, sess.ID, link.Ref.URL); err != nil || !removed {
		t.Fatalf("delete removed=%v err=%v", removed, err)
	}
	if removed, err := s.DeleteSessionPRLink(ctx, sess.ID, link.Ref.URL); err != nil || removed {
		t.Fatalf("second delete removed=%v err=%v, want a no-op", removed, err)
	}
}

func TestSessionPRLinkCap(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "ao")
	sess, err := s.CreateSession(ctx, sampleRecord("ao"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= domain.MaxSessionPRLinks; i++ {
		if _, err := s.SaveSessionPRLink(ctx, prLink(t, sess.ID, fmt.Sprintf("https://github.com/acme/app/pull/%d", i), domain.SessionPRReviewing), true); err != nil {
			t.Fatalf("link %d: %v", i, err)
		}
	}
	over := prLink(t, sess.ID, "https://github.com/acme/app/pull/999", domain.SessionPRReviewing)
	if _, err := s.SaveSessionPRLink(ctx, over, true); !errors.Is(err, domain.ErrSessionPRLinkLimit) {
		t.Fatalf("over cap err = %v, want ErrSessionPRLinkLimit", err)
	}
	// Relinking one already stored is not a new link.
	if _, err := s.SaveSessionPRLink(ctx, prLink(t, sess.ID, "https://github.com/acme/app/pull/1", domain.SessionPRContributing), true); err != nil {
		t.Fatalf("relink at cap: %v", err)
	}
}
