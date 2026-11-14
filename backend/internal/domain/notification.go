package domain

import (
	"errors"
	"time"
)

type NotificationType string

const (
	NotificationNeedsInput             NotificationType = "needs_input"
	NotificationReadyToMerge           NotificationType = "ready_to_merge"
	NotificationPRMerged               NotificationType = "pr_merged"
	NotificationPRClosedUnmerged       NotificationType = "pr_closed_unmerged"
	NotificationReviewCompleted        NotificationType = "review_completed"
	NotificationReviewChangesRequested NotificationType = "review_changes_requested"
)

func (t NotificationType) Valid() bool {
	switch t {
	case NotificationNeedsInput, NotificationReadyToMerge, NotificationPRMerged, NotificationPRClosedUnmerged,
		NotificationReviewCompleted, NotificationReviewChangesRequested:
		return true
	default:
		return false
	}
}

func (t NotificationType) NeedsResolution() bool {
	return t == NotificationNeedsInput || t == NotificationReadyToMerge
}

type NotificationStatus string

const (
	NotificationUnread NotificationStatus = "unread"
	NotificationRead   NotificationStatus = "read"
)

func (s NotificationStatus) Valid() bool {
	switch s {
	case NotificationUnread, NotificationRead:
		return true
	default:
		return false
	}
}

type NotificationListStatus string

const (
	NotificationListUnread     NotificationListStatus = "unread"
	NotificationListAll        NotificationListStatus = "all"
	NotificationListUnresolved NotificationListStatus = "unresolved"
)

func (s NotificationListStatus) Valid() bool {
	switch s {
	case NotificationListUnread, NotificationListAll, NotificationListUnresolved:
		return true
	default:
		return false
	}
}

type NotificationRecord struct {
	ID         string
	SessionID  SessionID
	ProjectID  ProjectID
	PRURL      string
	Type       NotificationType
	Title      string
	Body       string
	Status     NotificationStatus
	CreatedAt  time.Time
	ResolvedAt time.Time
	SourceKey  string
}

func (r NotificationRecord) Resolved() bool { return !r.ResolvedAt.IsZero() }

type NotificationEventKind string

const (
	NotificationCreated  NotificationEventKind = "created"
	NotificationResolved NotificationEventKind = "resolved"
	NotificationDeleted  NotificationEventKind = "deleted"
	NotificationCleared  NotificationEventKind = "cleared"
)

type NotificationEvent struct {
	Kind          NotificationEventKind
	Record        NotificationRecord
	ClearID       string
	ClearEpoch    string
	ClearSequence int64
}

var (
	ErrInvalidNotificationType   = errors.New("invalid notification type")
	ErrInvalidNotificationStatus = errors.New("invalid notification status")
	ErrInvalidNotificationRecord = errors.New("invalid notification record")
)

func (r NotificationRecord) Validate() error {
	if r.SessionID == "" || r.Title == "" || r.CreatedAt.IsZero() {
		return ErrInvalidNotificationRecord
	}
	if !r.Type.Valid() {
		return ErrInvalidNotificationType
	}
	if !r.Status.Valid() {
		return ErrInvalidNotificationStatus
	}
	return nil
}
