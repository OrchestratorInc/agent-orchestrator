package domain

// SessionVersionMove is recovery intent, not installation or display status.
// Only Retry permits automatic resume. A crash in Stopping or Starting needs
// explicit recovery because a replacement may be alive but not yet committed.
type SessionVersionMove struct {
	SessionID    SessionID
	Mode         SessionMode
	Harness      AgentHarness
	SourceLaunch string
	NativeID     string
	Version      string
	Stage        string
	Attempts     int
}

const (
	// VersionMoveStopping records intent before stopping the source process.
	VersionMoveStopping = "stopping"
	// VersionMoveStarting fences an uncommitted replacement from automatic retry.
	VersionMoveStarting = "starting"
	// VersionMoveRetry records a confirmed stop eligible for bounded recovery.
	VersionMoveRetry = "retry"
)

// NewSessionVersionMove captures the native identity of the source controller.
func NewSessionVersionMove(rec SessionRecord) SessionVersionMove {
	m := SessionVersionMove{SessionID: rec.ID, Mode: NormalizeSessionMode(rec.Mode), Harness: rec.Harness,
		SourceLaunch: rec.Metadata.RuntimeLaunchID, NativeID: rec.Metadata.AgentSessionID, Stage: VersionMoveStopping}
	if m.Mode == SessionModeChat {
		m.SourceLaunch, m.NativeID = rec.Metadata.ControllerGeneration, rec.Metadata.ProviderConversationID
	}
	if rec.StaleBinary != nil {
		m.Version = rec.StaleBinary.Version
	}
	return m
}
