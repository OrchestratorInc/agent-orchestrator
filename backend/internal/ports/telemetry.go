package ports

import (
	"context"
	"errors"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type TelemetryLevel string

const (
	TelemetryLevelDebug TelemetryLevel = "debug"
	TelemetryLevelInfo  TelemetryLevel = "info"
	TelemetryLevelWarn  TelemetryLevel = "warn"
	TelemetryLevelError TelemetryLevel = "error"
)

type TelemetryEvent struct {
	Name       string
	Source     string
	OccurredAt time.Time
	Level      TelemetryLevel
	ProjectID  *domain.ProjectID
	SessionID  *domain.SessionID
	RequestID  string
	Payload    map[string]any
}

type EventSink interface {
	Emit(ctx context.Context, ev TelemetryEvent)
	Close(ctx context.Context) error
}

type TelemetryIdentity struct {
	InstallID      string
	AnonymousID    string
	CloudUserID    string
	CloudUserKnown bool
	GitHubLogin    string
	OptedOut       bool
}

func (i TelemetryIdentity) DistinctID() string {
	if i.CloudUserID != "" {
		return i.CloudUserID
	}
	if i.AnonymousID != "" {
		return i.AnonymousID
	}
	return i.InstallID
}

var ErrInvalidCloudUserID = errors.New("invalid cloud user id")

type TelemetryIdentityStore interface {
	Snapshot() TelemetryIdentity
	OptedOut() bool
	ExportSuppressed() bool
	SetCloudUserID(ctx context.Context, id string) error
	MarkInstalledEvent(ctx context.Context) (bool, error)
	SetGitHubLogin(login string)
}

type InstallMilestones interface {
	Claim(name string) (first bool, known bool)
}

func FirstForInstall(m InstallMilestones, name string, noPriorRows bool) (first, ok bool) {
	if m == nil {
		return false, false
	}
	claimed, known := m.Claim(name)
	if !known {
		return false, false
	}
	return claimed && noPriorRows, true
}
