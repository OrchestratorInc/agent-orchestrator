package ports

import (
	"context"
	"errors"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TelemetryLevel is the severity of a telemetry event.
type TelemetryLevel string

const (
	// TelemetryLevelDebug marks verbose diagnostic events.
	TelemetryLevelDebug TelemetryLevel = "debug"
	// TelemetryLevelInfo marks normal operational events.
	TelemetryLevelInfo TelemetryLevel = "info"
	// TelemetryLevelWarn marks degraded but non-fatal events.
	TelemetryLevelWarn TelemetryLevel = "warn"
	// TelemetryLevelError marks failed operations.
	TelemetryLevelError TelemetryLevel = "error"
)

// TelemetryEvent is a structured operational/product event emitted by the
// daemon. Payload must be allowlisted at the call site; sinks may serialize it
// but must not mutate it.
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

// EventSink consumes structured telemetry events. Implementations should be
// best-effort: a slow or failing sink must not break the user action that
// emitted the event.
type EventSink interface {
	Emit(ctx context.Context, ev TelemetryEvent)
	Close(ctx context.Context) error
}

// TelemetryIdentity is who the remote telemetry stream is attributed to. The
// user fields are only ever filled from an explicit AO Cloud sign-in or the
// operator's authenticated GitHub account, never from git config, and are
// empty once the user has opted out.
type TelemetryIdentity struct {
	InstallID   string
	CloudUserID string
	GitHubLogin string
	// OptedOut is true when export must stop: the user opted out, or the identity
	// could not be persisted. Consumers (the paired phone) reset and stop capture.
	OptedOut bool
}

// DistinctID selects the signed-in Cloud user or the anonymous install.
func (i TelemetryIdentity) DistinctID() string {
	if i.CloudUserID != "" {
		return i.CloudUserID
	}
	return i.InstallID
}

// ErrInvalidCloudUserID is returned for an id that is not a plausible user id.
var ErrInvalidCloudUserID = errors.New("invalid cloud user id")

// TelemetryIdentityStore holds the live telemetry identity for the daemon.
type TelemetryIdentityStore interface {
	Snapshot() TelemetryIdentity
	// SetCloudUserID records the signed-in AO Cloud user ("" on sign-out). It
	// returns ErrInvalidCloudUserID when the id is not a plausible AO Cloud user id,
	// and another error when the identity state could not be made durable, in
	// which case remote export stays suppressed until a later call succeeds.
	SetCloudUserID(ctx context.Context, id string) error
	// MarkInstalledEvent reserves the one install event before queueing it.
	MarkInstalledEvent(ctx context.Context) (bool, error)
	SetGitHubLogin(login string)
}
