package domain

import (
	"fmt"
	"runtime"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/pkg/handoffnote"
)

const (
	// HandoffNotePrefix opens every handoff note.
	HandoffNotePrefix = handoffnote.Prefix

	// TerminalTurnRetention is how many Terminal UI turns AO keeps per session.
	TerminalTurnRetention = 50
)

// TerminalTurn is a Terminal UI turn's request as the prompt hook reported it.
type TerminalTurn struct {
	SessionID       SessionID
	Harness         AgentHarness
	NativeSessionID string
	UserRequest     string
	RequestedAt     time.Time
}

// HandoffTurn is one turn of a session in the form the handoff note and the
// history command share, whichever UI it happened in.
type HandoffTurn struct {
	UserRequest   string       `json:"userRequest"`
	AgentResponse string       `json:"agentResponse"`
	At            time.Time    `json:"at"`
	Harness       AgentHarness `json:"harness,omitempty"`
}

// HandoffNoteInput is everything a handoff note is rendered from. Turns holds
// every turn AO knows about, oldest first; the builder decides what fits.
type HandoffNoteInput struct {
	SwitchID      string
	SourceHarness AgentHarness
	TargetHarness AgentHarness
	SessionTitle  string
	Project       string
	WorkspacePath string
	Branch        string
	At            time.Time
	OriginalTask  string
	ChangedFiles  []string
	// ArtifactDir is the session's artifact directory and Artifacts the files
	// in it, in the order the note lists them.
	ArtifactDir string
	Artifacts   []SessionArtifactFile
	Turns       []HandoffTurn
	Terminal    bool
	// HistoryFiles point at the agent's own record for an agent whose answers
	// AO did not record, because it reports no turns through hooks.
	HistoryFiles []HandoffHistoryFile
	// Fork, when set, makes this a fork's note: the agent starts a new session
	// copied from that one instead of taking it over.
	Fork *HandoffFork
	// TerminalOutput is the end of the session's terminal screen, for agents
	// whose turns AO may not have recorded.
	TerminalOutput string
	// Inherited is the note the session itself started with, when it was a
	// fork given its conversation as text: the conversation from before it,
	// which its own turns don't hold.
	Inherited string
}

// HandoffFork names the session a text-handoff fork was copied from.
type HandoffFork struct {
	SourceSessionID SessionID
	SourceTitle     string
}

// HandoffHistoryFile is one agent's own record of this session and how to read it.
type HandoffHistoryFile struct {
	Harness AgentHarness
	Path    string
	// Read says how to find the messages in the file.
	Read string
	// RequestsOnly marks an agent whose requests AO recorded but not its answers.
	RequestsOnly bool
}

// handoffNoteMaxChars is the note's length cap on this system. On Windows a
// whole command line is limited to 32,767 UTF-16 units, escaping included, and
// a note passed as an argument shares it with the agent's own arguments.
var handoffNoteMaxChars = handoffNoteCharLimit(runtime.GOOS)

func handoffNoteCharLimit(goos string) int {
	if goos == "windows" {
		return 16_000
	}
	return ProviderSwitchMaxBootstrapChars
}

// handoffFullHistory points the agent at the whole conversation, and
// handoffForkFullHistory a fork's agent at the original session's.
const (
	handoffFullHistory     = "Run `ao session history` to read the conversation in full when you need something this note does not show."
	handoffForkFullHistory = "Run `ao session history %s` to read the original session's conversation in full when you need something this note does not show."
)

// BuildHandoffNote renders the note a provider receives when it takes over a
// session, or a text-handoff fork's agent starts with (handoffnote.Build,
// which AO Cloud's agent switch shares). It is
// deterministic and never asks a model to summarise: the latest turn is kept,
// the five before it are clipped to a budget, up to fifteen older ones become
// one line each, and anything beyond that is only counted.
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
