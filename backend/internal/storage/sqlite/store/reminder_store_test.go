package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func sampleReminder(id string, session domain.SessionRecord, due time.Time) domain.Reminder {
	return domain.Reminder{
		ID: domain.ReminderID(id), SessionID: session.ID, ProjectID: session.ProjectID,
		Name: "nightly", Message: "check the nightly build", DueAt: due,
		CreatedBy: domain.ReminderByAgent, Status: domain.ReminderPending,
		CreatedAt: testNow, UpdatedAt: testNow,
	}
}

func TestReminderLifecycle(t *testing.T) {
	s, session, _ := seededChatConversation(t)
	ctx := context.Background()
	later, soon := testNow.Add(2*time.Hour), testNow.Add(time.Hour)
	for _, r := range []domain.Reminder{sampleReminder("rem-later", session, later), sampleReminder("rem-soon", session, soon)} {
		if err := s.InsertReminder(ctx, r, 10); err != nil {
			t.Fatalf("insert %s: %v", r.ID, err)
		}
	}
	pending, err := s.ListPendingReminders(ctx, session.ID)
	if err != nil || len(pending) != 2 || pending[0].ID != "rem-soon" || pending[1].ID != "rem-later" {
		t.Fatalf("pending = %+v, err %v", pending, err)
	}
	got := pending[1]
	if got.Name != "nightly" || got.Message != "check the nightly build" || !got.DueAt.Equal(later) ||
		got.CreatedBy != domain.ReminderByAgent || got.Status != domain.ReminderPending || got.ResolvedAt != nil {
		t.Fatalf("round trip = %+v", got)
	}

	due, err := s.ListDueReminders(ctx, soon, 10)
	if err != nil || len(due) != 1 || due[0].ID != "rem-soon" {
		t.Fatalf("due = %+v, err %v", due, err)
	}
	if ok, err := s.DeferReminder(ctx, "rem-soon", soon.Add(time.Minute), "busy", soon); err != nil || !ok {
		t.Fatalf("defer: ok=%v err=%v", ok, err)
	}
	if due, _ := s.ListDueReminders(ctx, soon, 10); len(due) != 0 {
		t.Fatalf("deferred reminder still due: %+v", due)
	}
	deferred, _, _ := s.GetReminder(ctx, "rem-soon")
	if deferred.Attempts != 1 || deferred.StatusReason != "busy" || !deferred.DueAt.Equal(soon.Add(time.Minute)) {
		t.Fatalf("deferred = %+v", deferred)
	}

	// Only one of a cancel and a delivery wins.
	if ok, err := s.ResolveReminder(ctx, "rem-soon", domain.ReminderDelivered, "", soon); err != nil || !ok {
		t.Fatalf("deliver: ok=%v err=%v", ok, err)
	}
	if ok, err := s.ResolveReminder(ctx, "rem-soon", domain.ReminderCancelled, "", soon); err != nil || ok {
		t.Fatalf("cancel after delivery: ok=%v err=%v", ok, err)
	}
	delivered, found, err := s.GetReminder(ctx, "rem-soon")
	if err != nil || !found || delivered.Status != domain.ReminderDelivered || delivered.ResolvedAt == nil {
		t.Fatalf("delivered = %+v found=%v err=%v", delivered, found, err)
	}
	if _, found, _ := s.GetReminder(ctx, "rem-missing"); found {
		t.Fatal("found a reminder that does not exist")
	}
}

func TestReminderInsertEnforcesThePendingCap(t *testing.T) {
	s, session, _ := seededChatConversation(t)
	ctx := context.Background()
	if err := s.InsertReminder(ctx, sampleReminder("rem-1", session, testNow.Add(time.Hour)), 2); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertReminder(ctx, sampleReminder("rem-2", session, testNow.Add(time.Hour)), 2); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertReminder(ctx, sampleReminder("rem-3", session, testNow.Add(time.Hour)), 2); !errors.Is(err, domain.ErrReminderLimit) {
		t.Fatalf("third insert err = %v, want ErrReminderLimit", err)
	}
	// A resolved reminder frees its slot.
	if _, err := s.ResolveReminder(ctx, "rem-1", domain.ReminderCancelled, "", testNow); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertReminder(ctx, sampleReminder("rem-3", session, testNow.Add(time.Hour)), 2); err != nil {
		t.Fatalf("insert after cancel: %v", err)
	}
}

// The streak counts the agent's delivered reminders since a human last wrote.
func TestCountRemindersSinceHumanMessage(t *testing.T) {
	s, session, conversation := seededChatConversation(t)
	ctx := context.Background()
	deliver := func(id string, at time.Time) {
		t.Helper()
		if err := s.InsertReminder(ctx, sampleReminder(id, session, at), 10); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ResolveReminder(ctx, domain.ReminderID(id), domain.ReminderDelivered, "", at); err != nil {
			t.Fatal(err)
		}
	}
	count := func() int {
		t.Helper()
		n, err := s.CountRemindersSinceHumanMessage(ctx, session.ID)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	deliver("rem-1", testNow.Add(time.Minute))
	deliver("rem-2", testNow.Add(2*time.Minute))
	if got := count(); got != 2 {
		t.Fatalf("streak with no human message = %d, want 2", got)
	}
	seedTurn(t, s, conversation.ID, session.ID, "turn-human", "any news?", testNow.Add(3*time.Minute))
	if got := count(); got != 0 {
		t.Fatalf("streak after a human message = %d, want 0", got)
	}
	deliver("rem-3", testNow.Add(4*time.Minute))
	if got := count(); got != 1 {
		t.Fatalf("streak = %d, want 1", got)
	}
}
