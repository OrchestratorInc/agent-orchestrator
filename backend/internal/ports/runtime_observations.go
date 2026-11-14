package ports

import (
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type ProbeResult string

const (
	ProbeAlive  ProbeResult = "alive"
	ProbeDead   ProbeResult = "dead"
	ProbeFailed ProbeResult = "failed"
)

type RuntimeFacts struct {
	ObservedAt time.Time
	Runtime    ProbeResult
	Workload   ProbeResult
	LaunchID   string
}

type ActivitySignal struct {
	Valid                        bool
	State                        domain.ActivityState
	Timestamp                    time.Time
	ExpectedRevision             *int64
	ExpectedHarness              domain.AgentHarness
	Event                        string
	ToolName                     string
	ToolUseID                    string
	SubagentID                   string
	RunningSubagentIDs           *[]string
	AgentSessionID               string
	LatestUserPrompt             string
	LatestAssistantUpdate        string
	FinalAssistantMessage        string
	TurnUserRequest              string
	ConversationCheckpointOrigin domain.ConversationCheckpointOrigin
	CoordinationID               string
	ProviderTurnID               string
	SubmissionID                 string
	TranscriptPath               string
	LaunchID                     string
	NativeEntrypoint             string
	ProcessID                    int
	ControllerGeneration         string
}
