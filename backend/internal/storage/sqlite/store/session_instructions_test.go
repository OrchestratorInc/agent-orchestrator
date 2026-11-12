package store_test

import (
	"context"
	"testing"
)

func TestSessionAdditionalInstructionsPersistence(t *testing.T) {
	ctx := context.Background()
	st, session, _ := seededChatConversation(t)
	text, err := st.GetSessionAdditionalInstructions(ctx, session.ID)
	if err != nil || text != "" {
		t.Fatalf("legacy default = %q, %v", text, err)
	}
	if err := st.SetSessionAdditionalInstructions(ctx, session.ID, "规则"); err != nil {
		t.Fatal(err)
	}
	rec, found, err := st.GetSession(ctx, session.ID)
	if err != nil || !found || rec.Metadata.AdditionalInstructions != "规则" {
		t.Fatalf("session read = %+v, %v", rec.Metadata, err)
	}
	// A stale general session write must not overwrite the narrow policy writer.
	if err := st.UpdateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	text, err = st.GetSessionAdditionalInstructions(ctx, session.ID)
	if err != nil || text != "规则" {
		t.Fatalf("stale save lost policy = %q, %v", text, err)
	}
	if err := st.SetSessionAdditionalInstructions(ctx, session.ID, ""); err != nil {
		t.Fatal(err)
	}
	text, err = st.GetSessionAdditionalInstructions(ctx, session.ID)
	if err != nil || text != "" {
		t.Fatalf("clear = %q, %v", text, err)
	}
}
