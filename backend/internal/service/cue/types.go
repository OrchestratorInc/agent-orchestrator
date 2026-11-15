package cue

import (
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/shellterm"
)

type Input struct {
	Name    string
	Type    domain.CueType
	Command string
	Prompt  string
}

type InvokeInput struct {
	SessionID          domain.SessionID
	Shell              string
	AllowDirectCommand bool
}

type InvokeResult struct {
	Kind      domain.CueType
	SessionID domain.SessionID
	Terminal  *shellterm.ShellTerminal
}
