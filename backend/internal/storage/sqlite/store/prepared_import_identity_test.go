package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// A preparer's copy replaces the source identity before the first launch. The
// session and a conversation opened for it bind the copy; the source stays.
func TestPreparedImportIdentityMovesSessionAndConversation(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	seedProject(t, s, "prep")
	rec := sampleRecord("prep")
	rec.Mode = domain.SessionModeChat
	rec.Metadata.AgentSessionID, rec.Metadata.ProviderConversationID = "original", "original"
	rec.Metadata.ImportSource = &domain.SessionImportSource{NativeID: "original", ConfigDir: "/provider"}
	created, _, err := s.CreateImportedSession(ctx, rec, []domain.ImportedMessage{{Role: domain.MessageRoleUser, Text: "hello", At: time.Now()}})
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := s.OpenNativeConversation(ctx, "conv-prep", domain.ConversationScopeSession, "prep", created.ID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	next := *created.Metadata.ImportSource
	next.ResumeNativeID, next.ResumeCwd = "copy", "/workspace"
	stale := next
	stale.NativeID = "other"
	if ok, err := s.SetPreparedImportIdentity(ctx, created.ID, &stale, &next, created.Metadata, time.Now()); err != nil || ok {
		t.Fatalf("stale source accepted = %v, %v", ok, err)
	}
	if ok, err := s.SetPreparedImportIdentity(ctx, created.ID, created.Metadata.ImportSource, &next, created.Metadata, time.Now()); err != nil || !ok {
		t.Fatalf("prepared identity = %v, %v", ok, err)
	}
	loaded, _, err := s.GetSession(ctx, created.ID)
	if err != nil || loaded.Metadata.AgentSessionID != "copy" || loaded.Metadata.ProviderConversationID != "copy" ||
		loaded.Metadata.ImportSource.ResumeNativeID != "copy" || loaded.Metadata.ImportSource.NativeID != "original" {
		t.Fatalf("prepared session = %#v, %v", loaded.Metadata, err)
	}
	branch, err := s.ConversationBranch(ctx, conversation.ID, conversation.ActiveBranchID)
	if err != nil || branch.ProviderConversationID != "copy" {
		t.Fatalf("root branch = %#v, %v", branch, err)
	}
}
