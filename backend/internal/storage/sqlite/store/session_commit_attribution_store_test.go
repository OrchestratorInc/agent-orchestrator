package store_test

import (
	"context"
	"testing"
)

func TestSessionCommitAttributionRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	rec, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.SessionCommitAttribution(ctx, rec.ID); err != nil || got != "" {
		t.Fatalf("no row: got %q, %v; want \"\"", got, err)
	}
	const coAuthor = "Orchestrator <1+orchestrator[bot]@users.noreply.github.com>"
	if err := s.SetSessionCommitAttribution(ctx, rec.ID, coAuthor); err != nil {
		t.Fatal(err)
	}
	if got, err := s.SessionCommitAttribution(ctx, rec.ID); err != nil || got != coAuthor {
		t.Fatalf("got %q, %v; want %q", got, err, coAuthor)
	}
	if err := s.SetSessionCommitAttribution(ctx, rec.ID, coAuthor); err != nil {
		t.Fatalf("recording again: %v", err)
	}
	if err := s.SetSessionCommitAttribution(ctx, "missing-session", coAuthor); err == nil {
		t.Fatal("recorded a choice for a session that does not exist")
	}
}

func TestSessionPRFooterRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	rec, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatal(err)
	}
	if footer, recorded, err := s.SessionPRFooter(ctx, rec.ID); err != nil || footer || recorded {
		t.Fatalf("no row: got %v, %v, %v; want not recorded", footer, recorded, err)
	}
	// A row written for the trailer alone, as before the footer was kept,
	// has no footer choice.
	const coAuthor = "Orchestrator <1+orchestrator[bot]@users.noreply.github.com>"
	if err := s.SetSessionCommitAttribution(ctx, rec.ID, coAuthor); err != nil {
		t.Fatal(err)
	}
	if _, recorded, err := s.SessionPRFooter(ctx, rec.ID); err != nil || recorded {
		t.Fatalf("trailer only: recorded = %v, %v; want not recorded", recorded, err)
	}
	for _, want := range []bool{true, false} {
		if err := s.SetSessionPRFooter(ctx, rec.ID, want); err != nil {
			t.Fatal(err)
		}
		if footer, recorded, err := s.SessionPRFooter(ctx, rec.ID); err != nil || !recorded || footer != want {
			t.Fatalf("got %v, %v, %v; want %v recorded", footer, recorded, err, want)
		}
	}
	// Recording the footer keeps the trailer, and the other way round.
	if got, err := s.SessionCommitAttribution(ctx, rec.ID); err != nil || got != coAuthor {
		t.Fatalf("trailer after footer write = %q, %v; want %q", got, err, coAuthor)
	}
	if err := s.SetSessionCommitAttribution(ctx, rec.ID, ""); err != nil {
		t.Fatal(err)
	}
	if footer, recorded, _ := s.SessionPRFooter(ctx, rec.ID); !recorded || footer {
		t.Fatalf("footer after trailer write = %v recorded %v; want false recorded", footer, recorded)
	}
	if err := s.SetSessionPRFooter(ctx, "missing-session", true); err == nil {
		t.Fatal("recorded a footer choice for a session that does not exist")
	}
}

func TestSessionAttributionDecided(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	rec, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatal(err)
	}
	if decided, err := s.SessionAttributionDecided(ctx, rec.ID); err != nil || decided {
		t.Fatalf("no row: decided = %v, %v; want false", decided, err)
	}
	// A decision without a trailer still counts.
	if err := s.SetSessionCommitAttribution(ctx, rec.ID, ""); err != nil {
		t.Fatal(err)
	}
	if decided, err := s.SessionAttributionDecided(ctx, rec.ID); err != nil || !decided {
		t.Fatalf("after recording no trailer: decided = %v, %v; want true", decided, err)
	}
	if got, err := s.SessionCommitAttribution(ctx, rec.ID); err != nil || got != "" {
		t.Fatalf("trailer = %q, %v; want none", got, err)
	}
}
