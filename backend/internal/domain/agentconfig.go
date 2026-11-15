package domain

import "fmt"

type PermissionMode string

const (
	PermissionModeDefault           PermissionMode = "default"
	PermissionModeAcceptEdits       PermissionMode = "accept-edits"
	PermissionModeAuto              PermissionMode = "auto"
	PermissionModeBypassPermissions PermissionMode = "bypass-permissions"
)

type AgentConfig struct {
	Model       string             `json:"model,omitempty"`
	Effort      string             `json:"effort,omitempty"`
	Mode        string             `json:"mode,omitempty"`
	Permissions PermissionMode     `json:"permissions,omitempty"`
	Launch      AgentLaunchOptions `json:"-"`
}

type AgentLaunchOptions struct {
	FastMode          bool   `json:"fastMode,omitempty"`
	FastModeOff       bool   `json:"fastModeOff,omitempty"`
	Ultracode         bool   `json:"ultracode,omitempty"`
	AutoCompactWindow string `json:"autoCompactWindow,omitempty"`
	HandoffNote       string `json:"handoffNote,omitempty"`
}

var AutoCompactTokens = map[string]int{"200k": 200_000, "1m": 1_000_000}

func (c AgentConfig) IsZero() bool {
	return c == AgentConfig{}
}

func (m PermissionMode) Valid() bool {
	switch m {
	case "", PermissionModeDefault, PermissionModeAcceptEdits,
		PermissionModeAuto, PermissionModeBypassPermissions:
		return true
	default:
		return false
	}
}

func (c AgentConfig) Validate() error {
	switch c.Mode {
	case "", "low", "medium", "high", "ultra":
	default:
		return fmt.Errorf("invalid mode %q: want one of low, medium, high, ultra", c.Mode)
	}
	if c.Permissions.Valid() {
		return nil
	}
	return fmt.Errorf("invalid permissions %q: choose a supported permission mode", c.Permissions)
}
