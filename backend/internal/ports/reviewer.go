package ports

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type Reviewer interface {
	ReviewCommand(ctx context.Context, inv ReviewInvocation) (ReviewCommandSpec, error)
	ReviewMessage(ctx context.Context, inv ReviewInvocation) (string, error)
}

type ReviewerChatProfile interface {
	ReviewChatHarness() domain.AgentHarness
}

type ReviewCancelMode string

const (
	ReviewCancelInterrupt ReviewCancelMode = "interrupt"
	ReviewCancelMessage   ReviewCancelMode = "message"
	ReviewCancelInput     ReviewCancelMode = "input"
)

type ReviewCancelSpec struct {
	Mode       ReviewCancelMode
	Interrupts int
	Message    string
	Input      string
	Inputs     []string
	InputDelay time.Duration
}

type ReviewerCanceller interface {
	ReviewCancel(ctx context.Context) (ReviewCancelSpec, error)
}

type ReviewerRestorer interface {
	ReviewRestoreCommand(ctx context.Context, inv ReviewInvocation) (cmd ReviewCommandSpec, ok bool, err error)
}

type ReviewerReusePolicy interface {
	ReviewProcessReusable() bool
}

type ReviewerPromptReadinessProvider interface {
	ReviewPromptReadinessHints(ctx context.Context) (PromptReadinessHints, error)
}

type ReviewInvocation struct {
	ReviewerID       string
	RunID            string
	WorkerSessionID  domain.SessionID
	AgentSessionID   string
	PRURL            string
	TargetSHA        string
	ReviewQueue      []ReviewTask
	ReviewIndex      int
	Config           domain.AgentConfig
	WorkspacePath    string
	DataDir          string
	RunFilePath      string
	Prompt           string
	SystemPrompt     string
	SystemPromptFile string
	TaskPromptFile   string
	TaskPromptRoot   string
}

type ReviewTask struct {
	RunID     string
	PRURL     string
	TargetSHA string
}

type ReviewCommandSpec struct {
	Argv             []string
	Env              map[string]string
	AgentSessionID   string
	NativeResumed    bool
	InitialMessage   string
	WorkingDirectory string
}

type ReviewerResolver interface {
	Reviewer(harness domain.ReviewerHarness) (Reviewer, bool)
}
