package ports

import (
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// NotificationIntent is the lifecycle-to-notification-producer contract. It is
// not an HTTP DTO; lifecycle fills it from facts it already has after the
// underlying session/PR state write succeeds.
type NotificationIntent struct {
	Type      domain.NotificationType
	SessionID domain.SessionID
	ProjectID domain.ProjectID
	PRURL     string
	CreatedAt time.Time
	// SourceKey makes one terminal fact idempotent across retries and restarts.
	SourceKey string

	// Enrichment hints. These avoid storage reads on the hot path.
	SessionDisplayName string
	PRNumber           int
	PRTitle            string
	PRSourceBranch     string
	PRTargetBranch     string
	Provider           string
	Repo               string

	// ApprovalCategory is a browser approval's kind (payment, message, ...).
	// Only the kind and site reach the notification: the approval's summary
	// and reason are agent- and page-supplied and can carry typed text.
	ApprovalCategory string
	// ApprovalSite is the origin the approval acts on.
	ApprovalSite string
}

// NotificationResolution is the lifecycle-to-notification-producer contract for
// closing notifications whose underlying issue went away. Lifecycle fills it
// after the durable fact that resolves the issue is written — the session left
// the needs-input family, the PR stopped waiting on a merge.
//
// PRURL wins when set (a PR-scoped notification can outlive its session's
// activity churn); otherwise the resolution is scoped to SessionID.
type NotificationResolution struct {
	Type      domain.NotificationType
	SessionID domain.SessionID
	PRURL     string
	// SourceKey, when set, closes only the notification with that key (one
	// browser approval among a session's several). It wins over PRURL.
	SourceKey  string
	ResolvedAt time.Time
}
