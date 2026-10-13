package domain

import (
	"fmt"
	"regexp"
)

// SandboxMode selects where a session's agent process runs (#6663).
type SandboxMode string

// Sandbox modes. The empty mode means off.
const (
	SandboxModeOff      SandboxMode = "off"
	SandboxModeShared   SandboxMode = "shared"
	SandboxModePerAgent SandboxMode = "per-agent"
)

// SandboxConfig runs a project's agent processes inside a local Docker
// container: one per session (per-agent) or one per project (shared).
type SandboxConfig struct {
	Mode      SandboxMode `json:"mode,omitempty"`
	Image     string      `json:"image,omitempty"`
	CPUs      string      `json:"cpus,omitempty"`
	Memory    string      `json:"memory,omitempty"`
	PidsLimit int         `json:"pidsLimit,omitempty"`
}

var (
	// sandboxImagePattern also rejects a leading '-' so an image can never be
	// read as a docker flag.
	sandboxImagePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]{0,254}$`)
	sandboxCPUsPattern   = regexp.MustCompile(`^\d+(\.\d+)?$`)
	sandboxMemoryPattern = regexp.MustCompile(`^\d+[bkmgBKMG]?$`)
)

// Enabled reports whether agents should run inside a container.
func (c SandboxConfig) Enabled() bool {
	return c.Mode == SandboxModeShared || c.Mode == SandboxModePerAgent
}

// Validate checks the mode and the values passed to `docker run`.
func (c SandboxConfig) Validate() error {
	switch c.Mode {
	case "", SandboxModeOff:
		return nil
	case SandboxModeShared, SandboxModePerAgent:
	default:
		return fmt.Errorf("sandbox.mode: unknown mode %q", c.Mode)
	}
	if !sandboxImagePattern.MatchString(c.Image) {
		return fmt.Errorf("sandbox.image: %q is not a valid image reference", c.Image)
	}
	if c.CPUs != "" && !sandboxCPUsPattern.MatchString(c.CPUs) {
		return fmt.Errorf("sandbox.cpus: %q must be a number such as 2 or 1.5", c.CPUs)
	}
	if c.Memory != "" && !sandboxMemoryPattern.MatchString(c.Memory) {
		return fmt.Errorf("sandbox.memory: %q must look like 512m or 4g", c.Memory)
	}
	if c.PidsLimit < 0 {
		return fmt.Errorf("sandbox.pidsLimit: must not be negative")
	}
	return nil
}

// EffectiveSandbox returns the sandbox for a worker role. A role override replaces
// the project block wholesale. The orchestrator does not inherit the project
// block at all: it is sandboxed only by an explicit orchestrator.sandbox,
// which the session manager applies (#6663).
func EffectiveSandbox(base SandboxConfig, role RoleOverride) SandboxConfig {
	if role.Sandbox != nil {
		return *role.Sandbox
	}
	return base
}
