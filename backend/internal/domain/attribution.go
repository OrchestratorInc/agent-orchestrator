package domain

import "time"

type SessionCreator string

const (
	SessionCreatedByUser         SessionCreator = "user"
	SessionCreatedByOrchestrator SessionCreator = "orchestrator"
	SessionCreatedByScheduled    SessionCreator = "scheduled"
	SessionCreatedByAutomation   SessionCreator = "automation"
)

func (c SessionCreator) Valid() bool {
	switch c {
	case SessionCreatedByUser, SessionCreatedByOrchestrator, SessionCreatedByScheduled, SessionCreatedByAutomation:
		return true
	}
	return false
}

type RequestSurface string

const (
	RequestSurfaceDesktop RequestSurface = "desktop"
	RequestSurfaceMobile  RequestSurface = "mobile"
	RequestSurfaceCLI     RequestSurface = "cli"
)

type SessionTelemetryFacts struct {
	CreatedBy         SessionCreator
	WaitingInputCount int64
}

type AutoSpawnPause struct {
	Harness   AgentHarness `json:"harness"`
	Model     string       `json:"model,omitempty"`
	Effort    string       `json:"effort,omitempty"`
	Kind      string       `json:"kind,omitempty"`
	ErrorCode string       `json:"errorCode"`
	Message   string       `json:"message"`
	Failures  int          `json:"failures"`
	PausedAt  time.Time    `json:"pausedAt"`
}
