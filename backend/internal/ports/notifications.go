package ports

import (
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type NotificationIntent struct {
	Type      domain.NotificationType
	SessionID domain.SessionID
	ProjectID domain.ProjectID
	PRURL     string
	CreatedAt time.Time
	SourceKey string

	SessionDisplayName string
	PRNumber           int
	PRTitle            string
	PRSourceBranch     string
	PRTargetBranch     string
	Provider           string
	Repo               string
}

type NotificationResolution struct {
	Type       domain.NotificationType
	SessionID  domain.SessionID
	PRURL      string
	ResolvedAt time.Time
}
