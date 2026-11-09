package domain

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// ReminderID identifies a one-shot follow-up scheduled into a session.
type ReminderID string

// ReminderStatus is a reminder's durable lifecycle state.
type ReminderStatus string

// Reminder statuses. Only pending reminders can still fire or be cancelled.
const (
	ReminderPending   ReminderStatus = "pending"
	ReminderDelivered ReminderStatus = "delivered"
	ReminderCancelled ReminderStatus = "cancelled"
	// ReminderDropped means AO gave up on delivery, for example because the
	// session was archived or killed before the reminder came due.
	ReminderDropped ReminderStatus = "dropped"
)

// ReminderCreator records who scheduled a reminder.
type ReminderCreator string

// Reminder creators.
const (
	ReminderByAgent ReminderCreator = "agent"
	ReminderByUser  ReminderCreator = "user"
)

// Reminder bounds. The scheduler polls every few seconds, so one-minute
// granularity is what an agent can rely on.
const (
	MaxReminderMessageCharacters = 4000
	MaxReminderNameCharacters    = 120
	// MinReminderDelay keeps an agent from using reminders as a busy loop.
	MinReminderDelay = time.Minute
	// MaxReminderDelay bounds how far ahead a reminder can be scheduled.
	MaxReminderDelay = 30 * 24 * time.Hour
	// MaxPendingRemindersPerSession caps how many follow-ups one session can
	// have waiting at once.
	MaxPendingRemindersPerSession = 10
	// ReminderLoopWarningThreshold is how many self-scheduled reminders may
	// fire in a row with no human message before the delivered text tells the
	// agent it has been checking on its own.
	ReminderLoopWarningThreshold = 3
)

// Reminder validation errors.
var (
	ErrReminderMessageRequired = errors.New("reminder message is required")
	ErrReminderMessageTooLong  = errors.New("reminder message is too long")
	ErrReminderNameTooLong     = errors.New("reminder name is too long")
	ErrReminderTooSoon         = errors.New("reminder is due too soon")
	ErrReminderTooFar          = errors.New("reminder is due too far in the future")
	// ErrReminderLimit reports a session already holding the maximum number of
	// pending reminders.
	ErrReminderLimit = errors.New("session has too many pending reminders")
)

// Reminder is one durable one-shot follow-up bound to a single session. When
// it comes due, AO delivers Message back into that same session as a new turn.
type Reminder struct {
	ID           ReminderID
	SessionID    SessionID
	ProjectID    ProjectID
	Name         string
	Message      string
	DueAt        time.Time
	CreatedBy    ReminderCreator
	Status       ReminderStatus
	StatusReason string
	Attempts     int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ResolvedAt   *time.Time
}

// ValidateReminderContent checks the caller-provided parts of a reminder.
func ValidateReminderContent(name, message string, dueAt, now time.Time) error {
	if strings.TrimSpace(message) == "" {
		return ErrReminderMessageRequired
	}
	if utf8.RuneCountInString(message) > MaxReminderMessageCharacters {
		return ErrReminderMessageTooLong
	}
	if utf8.RuneCountInString(name) > MaxReminderNameCharacters {
		return ErrReminderNameTooLong
	}
	// A few seconds of slack lets "+1m" survive the round trip to the daemon.
	if dueAt.Before(now.Add(MinReminderDelay - 5*time.Second)) {
		return ErrReminderTooSoon
	}
	if dueAt.After(now.Add(MaxReminderDelay)) {
		return ErrReminderTooFar
	}
	return nil
}
