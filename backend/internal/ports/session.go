package ports

import (
	"context"
	"errors"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var ErrSessionNotFound = errors.New("session not found")

var ErrActivityProjectionContention = errors.New("activity projection contention")

type SpawnConfig struct {
	ProjectID           domain.ProjectID
	IssueID             domain.IssueID
	AutomationRunID     *domain.AutomationRunID
	ClientRequestID     string
	ClientRequestHash   string
	ParentSessionID     domain.SessionID
	CreatedBy           domain.SessionCreator
	TrackerProvider     domain.TrackerProvider
	IssueContext        string
	Kind                domain.SessionKind
	Harness             domain.AgentHarness
	Branch              string
	BaseBranch          string
	RequireNewBranch    bool
	Prompt              string
	AgentConfig         AgentConfig
	EffortOverride      bool
	AgentConfigResolved bool
	AgentAccountID      string

	RequestedMode domain.SessionMode

	RequestedOutput domain.RequestedOutput

	DisplayName         string
	DisplayNameExplicit bool
	NameFromPrompt      bool
	KeepDisplayName     bool
	Attachments         []SpawnAttachment

	Async           bool
	TaskPreparation domain.TaskPreparationToken
	Fork            *SpawnFork
}

type SpawnFork struct {
	SourceSessionID  domain.SessionID
	StartPoint       string
	SeedRef          string
	KeepSeedRef      bool
	SourceTitle      string
	ArtifactsAsOf    time.Time
	ArtifactsTurnID  string
	LimitAttachments bool
	AttachmentNames  []string
	SourceWorkspace  string
	PrepareChat      func(ctx context.Context, target ForkTarget) (providerConversationID string, err error)
	PrepareTerminal  func(ctx context.Context, target ForkTarget) (ForkTerminalLaunch, error)
	Handoff          func(ctx context.Context, target ForkTarget) (string, error)
}

type ForkTarget struct {
	SessionID        domain.SessionID
	DataDir          string
	WorkspacePath    string
	Env              map[string]string
	SystemPrompt     string
	SystemPromptFile string
}

type ForkTerminalLaunch struct {
	Argv           []string
	AgentSessionID string
}

type SpawnAttachment struct {
	Ext  string
	Data []byte
}
