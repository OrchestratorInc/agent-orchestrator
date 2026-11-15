package shellterm

import (
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type ShellTerminal struct {
	HandleID      string           `json:"handleId"`
	ProjectID     domain.ProjectID `json:"projectId,omitempty"`
	SessionID     domain.SessionID `json:"sessionId,omitempty"`
	WorkingDir    string           `json:"workingDir"`
	Title         string           `json:"title"`
	TerminalTitle string           `json:"terminalTitle,omitempty"`
	CreatedAt     time.Time        `json:"createdAt"`
}

type OpenShellTerminalInput struct {
	ProjectID     domain.ProjectID `json:"projectId,omitempty"`
	SessionID     domain.SessionID `json:"sessionId,omitempty"`
	Shell         string           `json:"shell,omitempty"`
	StartOnAttach bool             `json:"startOnAttach,omitempty"`
	Title         string           `json:"title,omitempty"`
	Cols          int              `json:"cols,omitempty"`
	Rows          int              `json:"rows,omitempty"`
}

type InitialInputReadyState struct {
	Text            string
	RawPrefix       string
	SubmitReadyText string
	SkipInput       bool
}

type OpenCommandTerminalInput struct {
	Owner                          string
	Argv                           []string
	HideFromList                   bool
	Env                            map[string]string
	WorkingDir                     string
	Title                          string
	InitialInput                   string
	InitialInputReadyStates        []InitialInputReadyState
	SendInitialInputOnReadyTimeout bool
}

type RunCueCommandInput struct {
	ProjectID domain.ProjectID
	SessionID domain.SessionID
	Shell     string
	Command   string
}

type CueCommandSessionTarget struct {
	ProjectID     domain.ProjectID
	WorkspacePath string
	Activity      domain.ActivityState
	IsTerminated  bool
}
