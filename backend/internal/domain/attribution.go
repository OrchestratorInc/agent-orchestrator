package domain

// SessionCreator records who caused a session to exist. It is a closed
// vocabulary so product telemetry can tell human-started work from work an
// agent or a schedule started on the user's behalf.
type SessionCreator string

// Session creators.
const (
	// SessionCreatedByUser is a person acting through the desktop app, the
	// mobile app, or the `ao` CLI outside any AO session.
	SessionCreatedByUser SessionCreator = "user"
	// SessionCreatedByOrchestrator is an AO orchestrator session running
	// `ao spawn`.
	SessionCreatedByOrchestrator SessionCreator = "orchestrator"
	// SessionCreatedByScheduled is an occurrence of a scheduled automation.
	SessionCreatedByScheduled SessionCreator = "scheduled"
	// SessionCreatedByAutomation is any other unattended source: tracker issue
	// intake, or a worker session spawning another session.
	SessionCreatedByAutomation SessionCreator = "automation"
)

// Valid reports whether c is one of the known creators.
func (c SessionCreator) Valid() bool {
	switch c {
	case SessionCreatedByUser, SessionCreatedByOrchestrator, SessionCreatedByScheduled, SessionCreatedByAutomation:
		return true
	}
	return false
}

// RequestSurface is the client a daemon request came from. It is derived by
// the HTTP layer from transport facts, never supplied by the caller.
type RequestSurface string

// Request surfaces.
const (
	RequestSurfaceDesktop RequestSurface = "desktop"
	RequestSurfaceMobile  RequestSurface = "mobile"
	// RequestSurfaceCLI is a native client with no browser origin on the
	// loopback listener: the `ao` CLI, or a local script calling the API.
	RequestSurfaceCLI RequestSurface = "cli"
)

// SessionTelemetryFacts are analytics facts kept beside a session row. Zero
// values mean unknown: sessions created before they were recorded have none.
type SessionTelemetryFacts struct {
	CreatedBy         SessionCreator
	WaitingInputCount int64
}
