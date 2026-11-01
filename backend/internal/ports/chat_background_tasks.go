package ports

import (
	"context"
	"errors"
	"time"
)

// ErrBackgroundTasksPartial accompanies an event-derived reading. It is not a
// complete provider inventory, including when no tasks have been observed.
var ErrBackgroundTasksPartial = errors.New("background task reporting is event-derived")

// ErrBackgroundTasksUnsupported distinguishes missing reporting from a known
// empty native task registry.
var ErrBackgroundTasksUnsupported = errors.New("provider cannot report background tasks")

// ChatBackgroundTask is native background work, never an OS process or a
// foreground tool call. IDs are scoped to one provider controller.
type ChatBackgroundTask struct {
	ID          string
	RunID       string
	Kind        string
	Description string
	State       string
	EndedAt     *time.Time
}

// ChatBackgroundTaskInspector reads the provider's complete live background
// set and any recently settled tasks. Reading must not start or wake a provider.
type ChatBackgroundTaskInspector interface {
	BackgroundTasks(context.Context) ([]ChatBackgroundTask, error)
}

// ChatBackgroundTaskDriver describes the reporting path provided by the
// conversations a driver creates. "native" can enumerate provider-owned work;
// "reported" observes explicit background events and returns partial readings.
// Runtime availability still depends on the installed provider version.
type ChatBackgroundTaskDriver interface {
	BackgroundTaskReporting() string
}
