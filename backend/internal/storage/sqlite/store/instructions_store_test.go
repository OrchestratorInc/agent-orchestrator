package store_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
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
// would grow the database for as long as dreaming runs. Each revision carries
// the user's lines, so the newest one still protects them after pruning.
func TestInstructionRevisionsKeepTheNewestHundred(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	at := time.Date(2026, time.October, 17, 9, 0, 0, 0, time.UTC)
	mine := []string{"Saved by the user.", "Also mine."}
	if _, _, err := store.SaveInstructionRevision(ctx, domain.InstructionsRevision{Body: "Saved by the user.\nAlso mine.", UserLines: mine, CreatedAt: at}, nil); err != nil {
		t.Fatalf("save user revision: %v", err)
	}
	for i := range 105 {
		body := fmt.Sprintf("Saved by the user.\nAlso mine.\nRule %d.", i)
		if _, _, err := store.SaveInstructionRevision(ctx, domain.InstructionsRevision{Body: body, UserLines: mine, Dreamed: true, CreatedAt: at}, nil); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}
	revisions, err := store.ListInstructionRevisions(ctx, "", 200)
	if err != nil {
		t.Fatalf("ListInstructionRevisions: %v", err)
	}
	if len(revisions) != 100 || !strings.HasSuffix(revisions[0].Body, "Rule 104.") || !strings.HasSuffix(revisions[99].Body, "Rule 5.") {
		t.Fatalf("kept %d revisions, newest %q oldest %q", len(revisions), revisions[0].Body, revisions[len(revisions)-1].Body)
	}
	if !reflect.DeepEqual(revisions[0].UserLines, mine) {
		t.Fatalf("user lines = %q, want %q", revisions[0].UserLines, mine)
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

// A dream is created as a background session and must read back as one, from
// a single lookup and from the full list the desktop polls.
func TestBackgroundSessionsRoundTrip(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	rec := sampleRecord("")
	rec.Background = true
	created, err := store.CreateSession(ctx, rec)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	got, ok, err := store.GetSession(ctx, created.ID)
	if err != nil || !ok || !got.Background {
		t.Fatalf("GetSession background=%v ok=%v err=%v", got.Background, ok, err)
	}
	all, err := store.ListAllSessions(ctx)
	if err != nil {
		t.Fatalf("ListAllSessions: %v", err)
	}
	for _, session := range all {
		if session.ID == created.ID && !session.Background {
			t.Fatalf("listed session lost its background flag")
		}
	}
}

// A dream learns verified project facts from the commands the agent ran: each
// command that passed or failed, newest first, never one still running.
func TestSessionCommandsReportFinishedCommandsNewestFirst(t *testing.T) {
	store, session, conversation := seededChatConversation(t)
	ctx := context.Background()
	for i, activity := range []domain.ConversationActivity{
		{ID: "a1", Kind: domain.ActivityKindCommand, Status: domain.ActivityStatusFailed, Summary: "make test", ProviderItemID: "item-1"},
		{ID: "a2", Kind: domain.ActivityKindCommand, Status: domain.ActivityStatusCompleted, Summary: "python3 -m unittest", ProviderItemID: "item-2"},
		{ID: "a3", Kind: domain.ActivityKindCommand, Status: domain.ActivityStatusRunning, Summary: "sleep 100", ProviderItemID: "item-3"},
		{ID: "a4", Kind: domain.ActivityKindPlan, Status: domain.ActivityStatusCompleted, Summary: "Plan the fix", ProviderItemID: "item-4"},
	} {
		if err := store.UpsertActivity(ctx, conversation.ID, "", activity, testNow.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatalf("upsert %s: %v", activity.ID, err)
		}
	}
	commands, err := store.SessionCommands(ctx, session.ID, 10)
	if err != nil {
		t.Fatalf("SessionCommands: %v", err)
	}
	want := []domain.SessionCommand{{Command: "python3 -m unittest"}, {Command: "make test", Failed: true}}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %#v, want %#v", commands, want)
	}
}

// AO closes a dream once its turns settle, so the store must report each turn
// of the session's own conversation, oldest first.
func TestSessionTurnStatesReportsTheConversationsTurns(t *testing.T) {
	store, session, conversation := seededChatConversation(t)
	ctx := context.Background()
	if states, err := store.SessionTurnStates(ctx, session.ID); err != nil || len(states) != 0 {
		t.Fatalf("turn states before any turn = %v, %v", states, err)
	}
	seedTurn(t, store, conversation.ID, session.ID, "turn-1", "Learn from it.", testNow)
	states, err := store.SessionTurnStates(ctx, session.ID)
	if err != nil || !reflect.DeepEqual(states, []domain.TurnState{domain.TurnStateCompleted}) {
		t.Fatalf("turn states = %v, %v; want one completed turn", states, err)
	}
}
