package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Terminating an imported session releases its conversation: the same chat
// imports again into a new session instead of returning the dead one.
func TestImportedConversationImportsAgainAfterTermination(t *testing.T) {
	s := newTestStore(t)
	seedProject(t, s, "again")
	ctx := context.Background()
	rec := sampleRecord("again")
	rec.Mode = domain.SessionModeChat
	rec.Activity.State = domain.ActivityIdle
	rec.Metadata.ImportSource = &domain.SessionImportSource{NativeID: "native-again", ConfigDir: "/provider/home", CWD: "/original/folder"}
	rec.Metadata.ProviderConversationID = "native-again"
	messages := []domain.ImportedMessage{{Role: domain.MessageRoleUser, Text: "hello", At: time.Now()}}
	first, fresh, err := s.CreateImportedSession(ctx, rec, messages)
	if err != nil || !fresh {
		t.Fatalf("first import: %v %v", fresh, err)
	}
	first.IsTerminated = true
	if err := s.UpdateSession(ctx, first); err != nil {
		t.Fatal(err)
	}
	second, fresh, err := s.CreateImportedSession(ctx, rec, messages)
	if err != nil || !fresh || second.ID == first.ID {
		t.Fatalf("import after termination: id=%s fresh=%v err=%v (first %s)", second.ID, fresh, err, first.ID)
	}
	again, fresh, err := s.CreateImportedSession(ctx, rec, messages)
	if err != nil || fresh || again.ID != second.ID {
		t.Fatalf("the live import must still be found: id=%s fresh=%v err=%v", again.ID, fresh, err)
	}
}
