package domain

import (
	"errors"
	"strings"
	"time"
)

type CueID string

type CueType string

const (
	CueTypeCommand CueType = "command"
	CueTypeAgent   CueType = "agent"
)

func (t CueType) Valid() bool {
	return t == CueTypeCommand || t == CueTypeAgent
}

const (
	MaxCueNameLength    = 64
	MaxCueCommandLength = 4 << 10
	MaxCuePromptLength  = 16 << 10
)

var (
	ErrInvalidCueType    = errors.New("invalid cue type")
	ErrInvalidCueName    = errors.New("invalid cue name")
	ErrInvalidCueCommand = errors.New("invalid cue command")
	ErrInvalidCuePrompt  = errors.New("invalid cue prompt")
	ErrCueNameExists     = errors.New("cue name already exists")
	ErrProjectUnknown    = errors.New("project not found")
)

type Cue struct {
	ID        CueID
	ProjectID ProjectID
	Name      string
	Type      CueType
	Command   string
	Prompt    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (c Cue) Validate() error {
	if strings.TrimSpace(c.Name) == "" || len(c.Name) > MaxCueNameLength {
		return ErrInvalidCueName
	}
	if !c.Type.Valid() {
		return ErrInvalidCueType
	}
	if len(c.Command) > MaxCueCommandLength {
		return ErrInvalidCueCommand
	}
	if len(c.Prompt) > MaxCuePromptLength {
		return ErrInvalidCuePrompt
	}
	switch c.Type {
	case CueTypeCommand:
		if strings.TrimSpace(c.Command) == "" {
			return ErrInvalidCueCommand
		}
	case CueTypeAgent:
		if strings.TrimSpace(c.Prompt) == "" {
			return ErrInvalidCuePrompt
		}
	}
	return nil
}
