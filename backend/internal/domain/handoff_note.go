package domain

import (
	"fmt"
	"runtime"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/pkg/handoffnote"
)

const (
	HandoffNotePrefix = handoffnote.Prefix

	TerminalTurnRetention = 50
)

type TerminalTurn struct {
	SessionID       SessionID
	Harness         AgentHarness
	NativeSessionID string
	UserRequest     string
	RequestedAt     time.Time
}

type HandoffTurn struct {
	UserRequest   string       `json:"userRequest"`
	AgentResponse string       `json:"agentResponse"`
	At            time.Time    `json:"at"`
	Harness       AgentHarness `json:"harness,omitempty"`
}

type HandoffNoteInput struct {
	SwitchID       string
	SourceHarness  AgentHarness
	TargetHarness  AgentHarness
	SessionTitle   string
	Project        string
	WorkspacePath  string
	Branch         string
	At             time.Time
	OriginalTask   string
	ChangedFiles   []string
	ArtifactDir    string
	Artifacts      []SessionArtifactFile
	Turns          []HandoffTurn
	Terminal       bool
	HistoryFiles   []HandoffHistoryFile
	Fork           *HandoffFork
	TerminalOutput string
	Inherited      string
}

type HandoffFork struct {
	SourceSessionID SessionID
	SourceTitle     string
}

type HandoffHistoryFile struct {
	Harness      AgentHarness
	Path         string
	Read         string
	RequestsOnly bool
}

var handoffNoteMaxChars = handoffNoteCharLimit(runtime.GOOS)

func handoffNoteCharLimit(goos string) int {
	if goos == "windows" {
		return 16_000
	}
	return ProviderSwitchMaxBootstrapChars
}

const (
	handoffFullHistory     = "Run `ao session history` to read the conversation in full when you need something this note does not show."
	handoffForkFullHistory = "Run `ao session history %s` to read the original session's conversation in full when you need something this note does not show."
)

func BuildHandoffNote(in HandoffNoteInput) string {
	turns := make([]handoffnote.Turn, len(in.Turns))
	for index, turn := range in.Turns {
		turns[index] = handoffnote.Turn{UserRequest: turn.UserRequest, AgentResponse: turn.AgentResponse, At: turn.At, Harness: string(turn.Harness)}
	}
	artifacts := make([]handoffnote.Artifact, len(in.Artifacts))
	for index, file := range in.Artifacts {
		artifacts[index] = handoffnote.Artifact{Path: file.Path, Size: file.Size, UpdatedAt: file.UpdatedAt}
	}
	history := make([]handoffnote.HistoryFile, len(in.HistoryFiles))
	for index, file := range in.HistoryFiles {
		history[index] = handoffnote.HistoryFile{Harness: string(file.Harness), Path: file.Path, Read: file.Read, RequestsOnly: file.RequestsOnly}
	}
	fullHistory := handoffFullHistory
	var fork *handoffnote.Fork
	if in.Fork != nil {
		fork = &handoffnote.Fork{SourceSessionID: string(in.Fork.SourceSessionID), SourceTitle: in.Fork.SourceTitle}
		fullHistory = fmt.Sprintf(handoffForkFullHistory, in.Fork.SourceSessionID)
	}
	return handoffnote.Build(handoffnote.Input{
		SwitchID: in.SwitchID, SourceHarness: string(in.SourceHarness), TargetHarness: string(in.TargetHarness),
		SessionTitle: in.SessionTitle, Project: in.Project, WorkspacePath: in.WorkspacePath, Branch: in.Branch,
		At: in.At, OriginalTask: in.OriginalTask, ChangedFiles: in.ChangedFiles,
		ArtifactDir: in.ArtifactDir, Artifacts: artifacts, Turns: turns, Terminal: in.Terminal,
		HistoryFiles: history, Fork: fork, TerminalOutput: in.TerminalOutput, Inherited: in.Inherited,
		FullHistory: fullHistory, MaxChars: handoffNoteMaxChars,
	})
}
