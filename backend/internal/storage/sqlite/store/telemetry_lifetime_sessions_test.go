package store_test

import (
	"context"
	"testing"
)

func TestLifetimeSessionsKeepsDeletedSessionsAndSkipsPreparations(t *testing.T) {
	s := newTestStore(t)
	seedProject(t, s, "mer")
	ctx := context.Background()
	first, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatal(err)
	}
	count, err := s.LifetimeSessions(ctx)
	if err != nil || count != 1 {
		t.Fatalf("count after creation = %d, %v", count, err)
	}
	if _, err := s.DeleteSession(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	count, err = s.LifetimeSessions(ctx)
	if err != nil || count != 1 {
		t.Fatalf("count after deletion = %d, %v", count, err)
	}
	preparation := sampleRecord("mer")
	preparation.IsTaskPreparation = true
	if _, err := s.CreateSession(ctx, preparation); err != nil {
		t.Fatal(err)
	}
	count, err = s.LifetimeSessions(ctx)
	if err != nil || count != 1 {
		t.Fatalf("count after preparation = %d, %v", count, err)
	}
}
