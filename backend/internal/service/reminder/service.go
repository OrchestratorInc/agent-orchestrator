// Package reminder owns one-shot follow-ups an agent schedules into its own
// session ("wake me later"). Unlike Automations, which spawn a new session on a
// recurring schedule, a reminder continues the session that created it: when
// it comes due, its message is delivered back into that conversation as a new
// turn through the same semantic delivery path worker reports use.
package reminder

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

const (
	dueScanLimit = 50
	// RetryDelay is how long a reminder waits after a delivery that could not
	// happen yet: a busy TUI turn, an unsent draft, or a transient failure.
	RetryDelay = time.Minute
	// maxFailedAttempts bounds retries for real delivery failures. Waiting on a
	// busy session never counts against it.
	maxFailedAttempts = 30
	maxReasonBytes    = 512
)

// ErrSessionBusy marks a delivery the session cannot take right now without
// interrupting its user or its running turn. The reminder is retried later.
var ErrSessionBusy = errors.New("session cannot take a follow-up right now")

// Store is the reminder service's persistence boundary.
type Store interface {
	GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error)
	InsertReminder(context.Context, domain.Reminder, int) error
	GetReminder(context.Context, domain.ReminderID) (domain.Reminder, bool, error)
	ListPendingReminders(context.Context, domain.SessionID) ([]domain.Reminder, error)
	ListDueReminders(context.Context, time.Time, int64) ([]domain.Reminder, error)
	ResolveReminder(context.Context, domain.ReminderID, domain.ReminderStatus, string, time.Time) (bool, error)
	DeferReminder(context.Context, domain.ReminderID, time.Time, string, time.Time) (bool, error)
	CountRemindersSinceHumanMessage(context.Context, domain.SessionID) (int, error)
}

// Delivery submits a semantic user-role turn into a session. A Chat session
// queues it behind a running turn; a hibernated one wakes to take it. The
// idempotency key makes a retried delivery land at most once.
type Delivery interface {
	Submit(ctx context.Context, id domain.SessionID, message, idempotencyKey string) error
}

// Service schedules, lists, cancels, and delivers reminders.
type Service struct {
	store    Store
	delivery Delivery
	now      func() time.Time
	newID    func() string
}

// Deps configures a Service.
type Deps struct {
	Store    Store
	Delivery Delivery
	Now      func() time.Time
	NewID    func() string
}

// New constructs a reminder Service.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.NewID == nil {
		d.NewID = func() string { return "rem_" + uuid.NewString() }
	}
	return &Service{store: d.Store, delivery: d.Delivery, now: d.Now, newID: d.NewID}
}

// CreateInput is a validated request to schedule one follow-up.
type CreateInput struct {
	SessionID domain.SessionID
	Name      string
	Message   string
	DueAt     time.Time
	CreatedBy domain.ReminderCreator
}

// Create schedules one pending reminder for a live session.
func (s *Service) Create(ctx context.Context, input CreateInput) (domain.Reminder, error) {
	if s == nil || s.store == nil {
		return domain.Reminder{}, errors.New("reminder: store is required")
	}
	if input.SessionID == "" {
		return domain.Reminder{}, apierr.Invalid("REMINDER_SESSION_REQUIRED", "Session id is required", nil)
	}
	now := s.now().UTC()
	name := strings.TrimSpace(input.Name)
	if err := domain.ValidateReminderContent(name, input.Message, input.DueAt, now); err != nil {
		return domain.Reminder{}, invalidReminder(err)
	}
	session, ok, err := s.store.GetSession(ctx, input.SessionID)
	if err != nil {
		return domain.Reminder{}, err
	}
	if !ok {
		return domain.Reminder{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	if session.IsTerminated {
		return domain.Reminder{}, apierr.Conflict("SESSION_TERMINATED", "Session is no longer running", nil)
	}
	createdBy := input.CreatedBy
	if createdBy == "" {
		createdBy = domain.ReminderByAgent
	}
	if createdBy != domain.ReminderByAgent && createdBy != domain.ReminderByUser {
		return domain.Reminder{}, apierr.Invalid("INVALID_REMINDER_CREATOR", "createdBy must be agent or user", nil)
	}
	rec := domain.Reminder{
		ID: domain.ReminderID(s.newID()), SessionID: session.ID, ProjectID: session.ProjectID,
		Name: name, Message: input.Message, DueAt: input.DueAt.UTC().Truncate(time.Second),
		CreatedBy: createdBy, Status: domain.ReminderPending, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.InsertReminder(ctx, rec, domain.MaxPendingRemindersPerSession); err != nil {
		if errors.Is(err, domain.ErrReminderLimit) {
			return domain.Reminder{}, apierr.Conflict("REMINDER_LIMIT",
				fmt.Sprintf("Session already has %d pending reminders; cancel one first", domain.MaxPendingRemindersPerSession), nil)
		}
		return domain.Reminder{}, err
	}
	return rec, nil
}

// ListSession returns a session's pending reminders, soonest first.
func (s *Service) ListSession(ctx context.Context, sessionID domain.SessionID) ([]domain.Reminder, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("reminder: store is required")
	}
	if sessionID == "" {
		return nil, apierr.Invalid("REMINDER_SESSION_REQUIRED", "Session id is required", nil)
	}
	return s.store.ListPendingReminders(ctx, sessionID)
}

// Cancel stops a pending reminder. A non-empty sessionID restricts the cancel
// to that session's own reminders, which is how ao remind cancel scopes it.
func (s *Service) Cancel(ctx context.Context, id domain.ReminderID, sessionID domain.SessionID) (domain.Reminder, error) {
	if s == nil || s.store == nil {
		return domain.Reminder{}, errors.New("reminder: store is required")
	}
	rec, ok, err := s.store.GetReminder(ctx, id)
	if err != nil {
		return domain.Reminder{}, err
	}
	if !ok || (sessionID != "" && rec.SessionID != sessionID) {
		return domain.Reminder{}, apierr.NotFound("REMINDER_NOT_FOUND", "Unknown reminder")
	}
	if rec.Status != domain.ReminderPending {
		return domain.Reminder{}, apierr.Conflict("REMINDER_NOT_PENDING", "Reminder already "+string(rec.Status), nil)
	}
	now := s.now().UTC()
	resolved, err := s.store.ResolveReminder(ctx, id, domain.ReminderCancelled, "", now)
	if err != nil {
		return domain.Reminder{}, err
	}
	if !resolved {
		return domain.Reminder{}, apierr.Conflict("REMINDER_NOT_PENDING", "Reminder is no longer pending", nil)
	}
	rec.Status, rec.ResolvedAt, rec.UpdatedAt = domain.ReminderCancelled, &now, now
	return rec, nil
}

// Tick delivers every reminder due at or before at. It runs on the automation
// scheduler's cadence; one failing reminder does not block the others.
func (s *Service) Tick(ctx context.Context, at time.Time) error {
	if s == nil || s.store == nil || s.delivery == nil {
		return errors.New("reminder: store and delivery are required")
	}
	due, err := s.store.ListDueReminders(ctx, at.UTC(), dueScanLimit)
	if err != nil {
		return err
	}
	var problems []error
	for _, rec := range due {
		if ctx.Err() != nil {
			break
		}
		if err := s.deliver(ctx, rec); err != nil {
			problems = append(problems, fmt.Errorf("reminder %s: %w", rec.ID, err))
		}
	}
	return errors.Join(problems...)
}

func (s *Service) deliver(ctx context.Context, rec domain.Reminder) error {
	now := s.now().UTC()
	session, ok, err := s.store.GetSession(ctx, rec.SessionID)
	if err != nil {
		return err
	}
	if !ok || session.IsTerminated || session.Activity.State == domain.ActivityExited {
		reason := "session was archived or killed before the reminder was due"
		if !ok {
			reason = "session no longer exists"
		}
		_, err := s.store.ResolveReminder(ctx, rec.ID, domain.ReminderDropped, reason, now)
		return err
	}
	// Chat queues a follow-up behind a running turn by itself. A terminal
	// session has no queue, so wait for the running turn rather than typing
	// into it.
	if domain.NormalizeSessionMode(session.Mode) != domain.SessionModeChat && session.Activity.State == domain.ActivityActive {
		_, err := s.store.DeferReminder(ctx, rec.ID, now.Add(RetryDelay), "waiting for the running turn to finish", now)
		return err
	}
	streak, err := s.store.CountRemindersSinceHumanMessage(ctx, rec.SessionID)
	if err != nil {
		return err
	}
	text := Render(rec, streak)
	if err := s.delivery.Submit(ctx, rec.SessionID, text, IdempotencyKey(rec.ID)); err != nil {
		if errors.Is(err, ErrSessionBusy) {
			_, deferErr := s.store.DeferReminder(ctx, rec.ID, now.Add(RetryDelay), truncate(err.Error()), now)
			return deferErr
		}
		if rec.Attempts+1 >= maxFailedAttempts {
			_, dropErr := s.store.ResolveReminder(ctx, rec.ID, domain.ReminderDropped, truncate("delivery kept failing: "+err.Error()), now)
			return errors.Join(err, dropErr)
		}
		_, deferErr := s.store.DeferReminder(ctx, rec.ID, now.Add(RetryDelay), truncate(err.Error()), now)
		return errors.Join(err, deferErr)
	}
	_, err = s.store.ResolveReminder(ctx, rec.ID, domain.ReminderDelivered, "", now)
	return err
}

// IdempotencyKey is the stable client message id of a reminder's delivery, so
// a retry after an uncertain send never lands twice.
func IdempotencyKey(id domain.ReminderID) string {
	return "reminder:" + string(id)
}

// Render formats the follow-up turn an agent receives. The header tells the
// agent this is the follow-up it scheduled, not a new request from a human.
// streak counts the agent's earlier reminders delivered since a human last
// wrote; past a threshold the text asks the agent to stop checking on its own.
func Render(rec domain.Reminder, streak int) string {
	var b strings.Builder
	b.WriteString("⏰ Scheduled follow-up")
	if rec.Name != "" {
		fmt.Fprintf(&b, " %q", rec.Name)
	}
	fmt.Fprintf(&b, " (%s), scheduled at %s for %s.", rec.ID,
		rec.CreatedAt.UTC().Format("2006-01-02 15:04 UTC"), rec.DueAt.UTC().Format("2006-01-02 15:04 UTC"))
	if rec.CreatedBy == domain.ReminderByUser {
		b.WriteString(" The user scheduled it for you.")
	} else {
		b.WriteString(" You scheduled it yourself; no human sent this message.")
	}
	b.WriteString("\n\n")
	b.WriteString(rec.Message)
	if rec.CreatedBy == domain.ReminderByAgent && streak+1 >= domain.ReminderLoopWarningThreshold {
		fmt.Fprintf(&b, "\n\nNote: this is self-scheduled follow-up %d in a row with no message from a human. "+
			"If nothing has changed, stop scheduling checks and tell the user what you are waiting on.", streak+1)
	}
	return b.String()
}

func invalidReminder(err error) error {
	switch {
	case errors.Is(err, domain.ErrReminderMessageRequired):
		return apierr.Invalid("REMINDER_MESSAGE_REQUIRED", "Reminder message is required", nil)
	case errors.Is(err, domain.ErrReminderMessageTooLong):
		return apierr.Invalid("REMINDER_MESSAGE_TOO_LONG", fmt.Sprintf("Reminder message must be at most %d characters", domain.MaxReminderMessageCharacters), nil)
	case errors.Is(err, domain.ErrReminderNameTooLong):
		return apierr.Invalid("REMINDER_NAME_TOO_LONG", fmt.Sprintf("Reminder name must be at most %d characters", domain.MaxReminderNameCharacters), nil)
	case errors.Is(err, domain.ErrReminderTooSoon):
		return apierr.Invalid("REMINDER_TOO_SOON", "Reminder must be at least 1 minute in the future", nil)
	case errors.Is(err, domain.ErrReminderTooFar):
		return apierr.Invalid("REMINDER_TOO_FAR", "Reminder must be at most 30 days in the future", nil)
	default:
		return apierr.Invalid("INVALID_REMINDER", err.Error(), nil)
	}
}

func truncate(s string) string {
	if len(s) <= maxReasonBytes {
		return s
	}
	return strings.ToValidUTF8(s[:maxReasonBytes], "")
}
