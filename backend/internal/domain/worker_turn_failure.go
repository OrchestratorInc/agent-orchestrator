package domain

// WorkerTurnFailure is a durable, not-yet-accepted signal for an orchestrator.
// It describes a failed turn, not whether the worker's assignment is complete.
type WorkerTurnFailure struct {
	TurnID          string
	SessionID       SessionID
	ProjectID       ProjectID
	DisplayName     string
	ErrorMessage    string
	Attempts        int64
	TargetSessionID SessionID
}
