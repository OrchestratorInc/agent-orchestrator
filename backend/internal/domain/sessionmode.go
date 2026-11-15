package domain

import "fmt"

type SessionMode string

const (
	SessionModeTUI  SessionMode = "tui"
	SessionModeChat SessionMode = "chat"
)

const DefaultSessionMode = SessionModeTUI

func (m SessionMode) Valid() bool {
	switch m {
	case SessionModeTUI, SessionModeChat:
		return true
	default:
		return false
	}
}

func NormalizeSessionMode(mode SessionMode) SessionMode {
	if mode.Valid() {
		return mode
	}
	return DefaultSessionMode
}

func ParseSessionMode(raw string) (SessionMode, error) {
	if raw == "" {
		return "", nil
	}
	mode := SessionMode(raw)
	if !mode.Valid() {
		return "", fmt.Errorf("unknown session mode %q: want %q or %q", raw, SessionModeChat, SessionModeTUI)
	}
	return mode, nil
}
