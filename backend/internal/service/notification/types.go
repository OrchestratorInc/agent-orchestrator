package notification

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

type TargetKind string

const (
	TargetSession TargetKind = "session"
	TargetPR      TargetKind = "pr"
)

type Target struct {
	Kind      TargetKind
	SessionID domain.SessionID
	PRURL     string
}

type Notification struct {
	domain.NotificationRecord
	Target Target
}

type ListStatus = domain.NotificationListStatus

const (
	ListUnread     = domain.NotificationListUnread
	ListAll        = domain.NotificationListAll
	ListUnresolved = domain.NotificationListUnresolved
)

type ListFilter struct {
	Status ListStatus
	Limit  int
	Cursor string
}

type ListPage struct {
	Notifications   []Notification
	NextCursor      string
	UnreadCount     int
	UnresolvedCount int
}
