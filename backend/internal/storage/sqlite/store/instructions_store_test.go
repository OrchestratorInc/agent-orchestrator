package store_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// The user's instructions and each project's are separate histories; a save
// against a stale revision must fail so a dream cannot overwrite a newer edit.
func TestInstructionRevisionsKeepScopesApartAndCheckTheRevision(t *testing.T) {
	store := newTestStore(t)
	seedProject(t, store, "notes")
	ctx := context.Background()
	at := time.Date(2026, time.October, 17, 9, 0, 0, 0, time.UTC)
	none := int64(0)

	user, created, err := store.SaveInstructionRevision(ctx, domain.InstructionsRevision{Body: "Answer first.", CreatedAt: at}, &none)
	if err != nil || !created || user.ID == 0 {
		t.Fatalf("save user instructions = %#v created=%v err=%v", user, created, err)
	}
	project, _, err := store.SaveInstructionRevision(ctx, domain.InstructionsRevision{ProjectID: "notes", Body: "Run python3 -m unittest.", CreatedAt: at}, &none)
	if err != nil {
		t.Fatalf("save project instructions: %v", err)
	}
	if body, err := store.CurrentInstructions(ctx, ""); err != nil || body != "Answer first." {
		t.Fatalf("user instructions = %q, %v", body, err)
	}
	if body, err := store.CurrentInstructions(ctx, "notes"); err != nil || body != "Run python3 -m unittest." {
		t.Fatalf("project instructions = %q, %v", body, err)
	}

	stale := none
	if _, _, err := store.SaveInstructionRevision(ctx, domain.InstructionsRevision{Body: "Lost.", CreatedAt: at}, &stale); !errors.Is(err, domain.ErrInstructionsChanged) {
		t.Fatalf("stale save err = %v, want ErrInstructionsChanged", err)
	}
	same, created, err := store.SaveInstructionRevision(ctx, domain.InstructionsRevision{Body: "Answer first.", CreatedAt: at.Add(time.Minute)}, &user.ID)
	if err != nil || created || same.ID != user.ID {
		t.Fatalf("unchanged save = %#v created=%v err=%v, want the existing revision", same, created, err)
	}
	dream, err := store.CreateSession(ctx, sampleRecord("notes"))
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	session := dream.ID
	next, created, err := store.SaveInstructionRevision(ctx, domain.InstructionsRevision{ProjectID: "notes", Body: "Run python3 -m unittest -v.", SessionID: &session, Dreamed: true, CreatedAt: at.Add(time.Hour)}, &project.ID)
	if err != nil || !created {
		t.Fatalf("dreamed save created=%v err=%v", created, err)
	}
	revisions, err := store.ListInstructionRevisions(ctx, "notes", 5)
	if err != nil {
		t.Fatalf("ListInstructionRevisions: %v", err)
	}
	want := []domain.InstructionsRevision{next, project}
	if !reflect.DeepEqual(revisions, want) {
		t.Fatalf("project revisions = %#v, want %#v", revisions, want)
	}
}

// History must stay bounded: every dream adds a revision, so unbounded rows
// would grow the database for as long as dreaming runs. The user's newest
// save must survive, or dreaming could drop the lines it protects.
func TestInstructionRevisionsKeepTheNewestTwenty(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	at := time.Date(2026, time.October, 17, 9, 0, 0, 0, time.UTC)
	if _, _, err := store.SaveInstructionRevision(ctx, domain.InstructionsRevision{Body: "Saved by the user.", CreatedAt: at}, nil); err != nil {
		t.Fatalf("save user revision: %v", err)
	}
	for i := range 25 {
		if _, _, err := store.SaveInstructionRevision(ctx, domain.InstructionsRevision{Body: fmt.Sprintf("Rule %d.", i), Dreamed: true, CreatedAt: at}, nil); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}
	revisions, err := store.ListInstructionRevisions(ctx, "", 100)
	if err != nil {
		t.Fatalf("ListInstructionRevisions: %v", err)
	}
	// The user's revision survives 25 dreams: it decides what dreaming keeps.
	if len(revisions) != 21 || revisions[0].Body != "Rule 24." || revisions[19].Body != "Rule 5." || revisions[20].Body != "Saved by the user." {
		t.Fatalf("kept %d revisions, newest %q oldest %q", len(revisions), revisions[0].Body, revisions[len(revisions)-1].Body)
	}
}

// Dreaming learns only from what the user typed: automation-sent prompts in
// the same conversation are not the user's words.
func TestHumanMessagesForSessionReturnsOnlyWhatTheUserTyped(t *testing.T) {
	store, session, conversation := seededChatConversation(t)
	ctx := context.Background()
	messages := []struct {
		id, text string
		origin   domain.MessageOrigin
	}{
		{"m1", "Fix the note ordering bug.", domain.MessageOriginHuman},
		{"m2", "Review queued by AO.", domain.MessageOriginAutomation},
		{"m3", "No, run python3 -m unittest instead of make test.", domain.MessageOriginHuman},
	}
	for i, message := range messages {
		if _, err := store.AppendUserMessage(ctx, conversation.ID, session.ID, "gen-1", domain.ConversationMessage{
			ID: message.id, Text: message.text, Origin: message.origin,
		}, "turn-"+message.id, testNow.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("append %s: %v", message.id, err)
		}
	}
	texts, found, err := store.HumanMessagesForSession(ctx, session.ID)
	if err != nil || !found {
		t.Fatalf("HumanMessagesForSession found=%v err=%v", found, err)
	}
	if want := []string{messages[0].text, messages[2].text}; !reflect.DeepEqual(texts, want) {
		t.Fatalf("human messages = %q, want %q", texts, want)
	}
	if _, found, err := store.HumanMessagesForSession(ctx, "terminal-only"); err != nil || found {
		t.Fatalf("session without a conversation found=%v err=%v", found, err)
	}
}
