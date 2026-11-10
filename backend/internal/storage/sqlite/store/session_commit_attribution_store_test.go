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
