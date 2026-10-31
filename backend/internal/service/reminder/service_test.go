package reminder

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

var now = time.Date(2026, time.October, 31, 6, 0, 0, 0, time.UTC)

type fakeStore struct {
	sessions  map[domain.SessionID]domain.SessionRecord
	reminders map[domain.ReminderID]domain.Reminder
	streak    int
	limit     int
}

func newFakeStore(sessions ...domain.SessionRecord) *fakeStore {
	s := &fakeStore{sessions: map[domain.SessionID]domain.SessionRecord{}, reminders: map[domain.ReminderID]domain.Reminder{}}
	for _, rec := range sessions {
		s.sessions[rec.ID] = rec
	}
	return s
}

func (s *fakeStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	rec, ok := s.sessions[id]
	return rec, ok, nil
}

func (s *fakeStore) InsertReminder(_ context.Context, r domain.Reminder, limit int) error {
	pending := 0
	for _, existing := range s.reminders {
		if existing.SessionID == r.SessionID && existing.Status == domain.ReminderPending {
			pending++
		}
	}
	s.limit = limit
	if pending >= limit {
		return domain.ErrReminderLimit
	}
	s.reminders[r.ID] = r
	return nil
}

func (s *fakeStore) GetReminder(_ context.Context, id domain.ReminderID) (domain.Reminder, bool, error) {
	r, ok := s.reminders[id]
	return r, ok, nil
}

func (s *fakeStore) ListPendingReminders(_ context.Context, id domain.SessionID) ([]domain.Reminder, error) {
	var out []domain.Reminder
	for _, r := range s.reminders {
		if r.SessionID == id && r.Status == domain.ReminderPending {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DueAt.Before(out[j].DueAt) })
	return out, nil
}

func (s *fakeStore) ListDueReminders(_ context.Context, at time.Time, _ int64) ([]domain.Reminder, error) {
	var out []domain.Reminder
	for _, r := range s.reminders {
		if r.Status == domain.ReminderPending && !r.DueAt.After(at) {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *fakeStore) ResolveReminder(_ context.Context, id domain.ReminderID, status domain.ReminderStatus, reason string, at time.Time) (bool, error) {
	r, ok := s.reminders[id]
	if !ok || r.Status != domain.ReminderPending {
		return false, nil
	}
	r.Status, r.StatusReason, r.ResolvedAt = status, reason, &at
	s.reminders[id] = r
	return true, nil
}

func (s *fakeStore) DeferReminder(_ context.Context, id domain.ReminderID, due time.Time, reason string, _ time.Time) (bool, error) {
	r, ok := s.reminders[id]
	if !ok || r.Status != domain.ReminderPending {
		return false, nil
	}
	r.DueAt, r.StatusReason = due, reason
	r.Attempts++
	s.reminders[id] = r
	return true, nil
}

func (s *fakeStore) CountRemindersSinceHumanMessage(context.Context, domain.SessionID) (int, error) {
	return s.streak, nil
}

type sent struct{ session, text, key string }

type fakeDelivery struct {
	sent []sent
	err  error
}

func (d *fakeDelivery) Submit(_ context.Context, id domain.SessionID, text, key string) error {
	if d.err != nil {
		return d.err
	}
	d.sent = append(d.sent, sent{string(id), text, key})
	return nil
}

func chatSession(id string) domain.SessionRecord {
	return domain.SessionRecord{ID: domain.SessionID(id), ProjectID: "proj", Mode: domain.SessionModeChat,
		Activity: domain.Activity{State: domain.ActivityIdle}}
}

func newService(store *fakeStore, delivery *fakeDelivery) (*Service, *time.Time) {
	clock := now
	ids := 0
	return New(Deps{Store: store, Delivery: delivery, Now: func() time.Time { return clock },
		NewID: func() string { ids++; return "rem_" + string(rune('0'+ids)) }}), &clock
}

func apiCode(err error) string {
	var apiErr *apierr.Error
	if errors.As(err, &apiErr) {
		return apiErr.Code
	}
	return ""
}

func TestCreateValidatesAndPersists(t *testing.T) {
	store := newFakeStore(chatSession("s1"), domain.SessionRecord{ID: "gone", ProjectID: "proj", IsTerminated: true})
	svc, _ := newService(store, &fakeDelivery{})
	ctx := context.Background()
	tests := []struct {
		name  string
		input CreateInput
		code  string
	}{
		{"no session", CreateInput{Message: "x", DueAt: now.Add(time.Hour)}, "REMINDER_SESSION_REQUIRED"},
		{"blank message", CreateInput{SessionID: "s1", Message: " ", DueAt: now.Add(time.Hour)}, "REMINDER_MESSAGE_REQUIRED"},
		{"too long", CreateInput{SessionID: "s1", Message: strings.Repeat("界", 4001), DueAt: now.Add(time.Hour)}, "REMINDER_MESSAGE_TOO_LONG"},
		{"name too long", CreateInput{SessionID: "s1", Name: strings.Repeat("n", 121), Message: "x", DueAt: now.Add(time.Hour)}, "REMINDER_NAME_TOO_LONG"},
		{"too soon", CreateInput{SessionID: "s1", Message: "x", DueAt: now.Add(30 * time.Second)}, "REMINDER_TOO_SOON"},
		{"in the past", CreateInput{SessionID: "s1", Message: "x", DueAt: now.Add(-time.Hour)}, "REMINDER_TOO_SOON"},
		{"too far", CreateInput{SessionID: "s1", Message: "x", DueAt: now.Add(31 * 24 * time.Hour)}, "REMINDER_TOO_FAR"},
		{"unknown session", CreateInput{SessionID: "nope", Message: "x", DueAt: now.Add(time.Hour)}, "SESSION_NOT_FOUND"},
		{"terminated session", CreateInput{SessionID: "gone", Message: "x", DueAt: now.Add(time.Hour)}, "SESSION_TERMINATED"},
		{"bad creator", CreateInput{SessionID: "s1", Message: "x", DueAt: now.Add(time.Hour), CreatedBy: "robot"}, "INVALID_REMINDER_CREATOR"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := svc.Create(ctx, tc.input); apiCode(err) != tc.code {
				t.Fatalf("err = %v, want %s", err, tc.code)
			}
		})
	}
	rec, err := svc.Create(ctx, CreateInput{SessionID: "s1", Name: "  CI  ", Message: "check CI", DueAt: now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if rec.ProjectID != "proj" || rec.Name != "CI" || rec.CreatedBy != domain.ReminderByAgent || rec.Status != domain.ReminderPending ||
		store.limit != domain.MaxPendingRemindersPerSession {
		t.Fatalf("created = %+v (limit %d)", rec, store.limit)
	}
}

func TestCreateRefusesPastThePendingCap(t *testing.T) {
	store := newFakeStore(chatSession("s1"))
	svc, _ := newService(store, &fakeDelivery{})
	for i := range domain.MaxPendingRemindersPerSession {
		store.reminders[domain.ReminderID(rune('a'+i))] = domain.Reminder{SessionID: "s1", Status: domain.ReminderPending}
	}
	if _, err := svc.Create(context.Background(), CreateInput{SessionID: "s1", Message: "x", DueAt: now.Add(time.Hour)}); apiCode(err) != "REMINDER_LIMIT" {
		t.Fatalf("err = %v", err)
	}
}

func TestCancelIsScopedToTheOwningSession(t *testing.T) {
	store := newFakeStore(chatSession("s1"), chatSession("s2"))
	svc, _ := newService(store, &fakeDelivery{})
	ctx := context.Background()
	rec, err := svc.Create(ctx, CreateInput{SessionID: "s1", Message: "x", DueAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Cancel(ctx, rec.ID, "s2"); apiCode(err) != "REMINDER_NOT_FOUND" {
		t.Fatalf("cross-session cancel err = %v", err)
	}
	cancelled, err := svc.Cancel(ctx, rec.ID, "s1")
	if err != nil || cancelled.Status != domain.ReminderCancelled {
		t.Fatalf("cancel = %+v, %v", cancelled, err)
	}
	if _, err := svc.Cancel(ctx, rec.ID, ""); apiCode(err) != "REMINDER_NOT_PENDING" {
		t.Fatalf("second cancel err = %v", err)
	}
	if pending, _ := svc.ListSession(ctx, "s1"); len(pending) != 0 {
		t.Fatalf("pending after cancel = %+v", pending)
	}
}

func TestTickDeliversDueRemindersIntoTheirSession(t *testing.T) {
	store := newFakeStore(chatSession("s1"))
	delivery := &fakeDelivery{}
	svc, clock := newService(store, delivery)
	ctx := context.Background()
	rec, err := svc.Create(ctx, CreateInput{SessionID: "s1", Name: "CI", Message: "check the worker's PR CI", DueAt: now.Add(30 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Tick(ctx, now.Add(29*time.Minute)); err != nil || len(delivery.sent) != 0 {
		t.Fatalf("early tick: err=%v sent=%+v", err, delivery.sent)
	}
	*clock = now.Add(30 * time.Minute)
	if err := svc.Tick(ctx, *clock); err != nil {
		t.Fatal(err)
	}
	if len(delivery.sent) != 1 || delivery.sent[0].session != "s1" || delivery.sent[0].key != "reminder:"+string(rec.ID) ||
		!strings.Contains(delivery.sent[0].text, "check the worker's PR CI") || !strings.HasPrefix(delivery.sent[0].text, "⏰ Scheduled follow-up \"CI\"") {
		t.Fatalf("sent = %+v", delivery.sent)
	}
	if got := store.reminders[rec.ID]; got.Status != domain.ReminderDelivered {
		t.Fatalf("status = %s", got.Status)
	}
	// It fires once.
	if err := svc.Tick(ctx, *clock); err != nil || len(delivery.sent) != 1 {
		t.Fatalf("second tick: err=%v sent=%d", err, len(delivery.sent))
	}
}

func TestTickDropsRemindersForEndedSessions(t *testing.T) {
	store := newFakeStore(chatSession("s1"))
	delivery := &fakeDelivery{}
	svc, _ := newService(store, delivery)
	ctx := context.Background()
	rec, _ := svc.Create(ctx, CreateInput{SessionID: "s1", Message: "x", DueAt: now.Add(time.Hour)})
	ended := store.sessions["s1"]
	ended.IsTerminated = true
	store.sessions["s1"] = ended
	if err := svc.Tick(ctx, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got := store.reminders[rec.ID]
	if len(delivery.sent) != 0 || got.Status != domain.ReminderDropped || !strings.Contains(got.StatusReason, "archived or killed") {
		t.Fatalf("reminder = %+v, sent = %+v", got, delivery.sent)
	}
}

func TestTickNeverInterruptsAndRetriesLater(t *testing.T) {
	ctx := context.Background()
	t.Run("busy terminal turn", func(t *testing.T) {
		busy := chatSession("s1")
		busy.Mode, busy.Activity.State = domain.SessionModeTUI, domain.ActivityActive
		store := newFakeStore(busy)
		delivery := &fakeDelivery{}
		svc, clock := newService(store, delivery)
		rec, _ := svc.Create(ctx, CreateInput{SessionID: "s1", Message: "x", DueAt: now.Add(time.Hour)})
		*clock = now.Add(time.Hour)
		if err := svc.Tick(ctx, *clock); err != nil {
			t.Fatal(err)
		}
		got := store.reminders[rec.ID]
		if len(delivery.sent) != 0 || got.Status != domain.ReminderPending || !got.DueAt.Equal(clock.Add(RetryDelay)) {
			t.Fatalf("reminder = %+v, sent = %+v", got, delivery.sent)
		}
	})
	t.Run("unsent draft", func(t *testing.T) {
		store := newFakeStore(chatSession("s1"))
		delivery := &fakeDelivery{err: ErrSessionBusy}
		svc, clock := newService(store, delivery)
		rec, _ := svc.Create(ctx, CreateInput{SessionID: "s1", Message: "x", DueAt: now.Add(time.Hour)})
		*clock = now.Add(time.Hour)
		if err := svc.Tick(ctx, *clock); err != nil {
			t.Fatalf("busy is not a failure: %v", err)
		}
		if got := store.reminders[rec.ID]; got.Status != domain.ReminderPending || !got.DueAt.After(*clock) {
			t.Fatalf("reminder = %+v", got)
		}
	})
	t.Run("repeated failures give up", func(t *testing.T) {
		store := newFakeStore(chatSession("s1"))
		delivery := &fakeDelivery{err: errors.New("provider down")}
		svc, clock := newService(store, delivery)
		rec, _ := svc.Create(ctx, CreateInput{SessionID: "s1", Message: "x", DueAt: now.Add(time.Hour)})
		*clock = now.Add(time.Hour)
		if err := svc.Tick(ctx, *clock); err == nil || store.reminders[rec.ID].Status != domain.ReminderPending {
			t.Fatalf("first failure: err=%v reminder=%+v", err, store.reminders[rec.ID])
		}
		r := store.reminders[rec.ID]
		r.Attempts = maxFailedAttempts - 1
		store.reminders[rec.ID] = r
		*clock = clock.Add(2 * RetryDelay)
		_ = svc.Tick(ctx, *clock)
		if got := store.reminders[rec.ID]; got.Status != domain.ReminderDropped || !strings.Contains(got.StatusReason, "provider down") {
			t.Fatalf("reminder = %+v", got)
		}
	})
}

func TestRenderWarnsALoopingAgent(t *testing.T) {
	rec := domain.Reminder{ID: "rem_1", Message: "check Sentry", CreatedBy: domain.ReminderByAgent, CreatedAt: now, DueAt: now.Add(time.Hour)}
	quiet := Render(rec, domain.ReminderLoopWarningThreshold-2)
	if strings.Contains(quiet, "in a row") || !strings.Contains(quiet, "You scheduled it yourself") ||
		!strings.Contains(quiet, "2026-10-31 07:00 UTC") || !strings.HasSuffix(quiet, "\n\ncheck Sentry") {
		t.Fatalf("render = %q", quiet)
	}
	loud := Render(rec, domain.ReminderLoopWarningThreshold-1)
	if !strings.Contains(loud, "follow-up 3 in a row with no message from a human") {
		t.Fatalf("render = %q", loud)
	}
	rec.CreatedBy = domain.ReminderByUser
	if user := Render(rec, 10); strings.Contains(user, "in a row") || !strings.Contains(user, "The user scheduled it") {
		t.Fatalf("user render = %q", user)
	}
}
