package store_test

import (
	"context"
	"testing"
)

func TestSessionProviderStampRoundTrip(t *testing.T) {
	s, sessionID, _ := conversationFixture(t)
	ctx := context.Background()

	rec, _, err := s.GetSession(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Metadata.ProviderBaseURL != "" || rec.Metadata.ProviderModel != "" {
		t.Fatalf("fresh session must have empty stamp, got %+v", rec.Metadata)
	}

	rec.Metadata.ProviderBaseURL = "https://gw.example"
	rec.Metadata.ProviderModel = "claude-x"
	if err := s.UpdateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.GetSession(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Metadata.ProviderBaseURL != "https://gw.example" || got.Metadata.ProviderModel != "claude-x" {
		t.Fatalf("stamp lost on update: %+v", got.Metadata)
	}
}
