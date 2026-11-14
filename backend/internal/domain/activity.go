package domain

import "time"

type ActivityState string

const (
	ActivityActive       ActivityState = "active"
	ActivityIdle         ActivityState = "idle"
	ActivityWaitingInput ActivityState = "waiting_input"
	ActivityBlocked      ActivityState = "blocked"
	ActivityExited       ActivityState = "exited"
)

func (a ActivityState) IsSticky() bool {
	return a == ActivityWaitingInput || a == ActivityBlocked
}

func (a ActivityState) NeedsInput() bool {
	return a == ActivityWaitingInput || a == ActivityBlocked
}

type Activity struct {
	State          ActivityState `json:"state"`
	LastActivityAt time.Time     `json:"lastActivityAt"`
}

type TurnFailureCandidate struct {
	ID                   SessionID
	Harness              AgentHarness
	Activity             Activity
	Revision             int64
	RuntimeLaunchID      string
	NativeTranscriptPath string
	LatestUserPromptAt   time.Time
}
