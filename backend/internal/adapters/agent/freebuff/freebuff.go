// Package freebuff prepares an unregistered native Freebuff TUI adapter.
// Public launch admission is deliberately blocked pending released-binary
// qualification. Public source is not proof of Freebuff 0.2.26 behavior.
package freebuff

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var (
	// ErrQualificationRequired prevents launch before native conformance.
	ErrQualificationRequired = errors.New("freebuff: released native launch, profile isolation, instructions and readiness require qualification")
	// ErrRestoreUnsupported prevents missing history from becoming fresh replay.
	ErrRestoreUnsupported = errors.New("freebuff: exact restore and renewed standing instructions are not qualified; refusing fresh replay")
)

// Plugin is source preparation only; it must not be registered in production.
type Plugin struct {
	agentbase.Base
}

// New returns an unregistered, qualification-gated adapter.
func New() *Plugin { return &Plugin{} }

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)

// Manifest identifies the unregistered candidate.
func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID: "freebuff", Name: "Freebuff", Version: "0.0.1",
		Description:  "Unqualified Freebuff native TUI preparation.",
		Capabilities: []adapters.Capability{adapters.CapabilityAgent},
	}
}

// GetLaunchCommand validates supported candidate inputs but never admits a
// launch. Do not remove this gate before proving the released native contract.
func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	if _, err := candidateLaunchCommand(ctx, cfg); err != nil {
		return nil, err
	}
	return nil, ErrQualificationRequired
}

// candidateLaunchCommand keeps task text out of argv. The public-source Freebuff
// parser has --cwd but no initial prompt or --agent override. No CLI is executed.
func candidateLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if cfg.Config.Model != "" || cfg.Config.Effort != "" || cfg.Config.Mode != "" {
		return nil, fmt.Errorf("freebuff: model, effort and mode overrides are unsupported; preserve native selection")
	}
	for _, mode := range []ports.PermissionMode{cfg.Permissions, cfg.Config.Permissions} {
		if mode != "" && mode != ports.PermissionModeDefault {
			return nil, fmt.Errorf("freebuff: requested permission mapping is unproved")
		}
	}
	if len(cfg.AllowedTools) != 0 || len(cfg.DisallowedTools) != 0 {
		return nil, fmt.Errorf("freebuff: tool restrictions are unsupported")
	}
	if cfg.NativeSessionID != "" {
		return nil, fmt.Errorf("freebuff: caller-assigned native identity is unsupported")
	}
	if cfg.SystemPrompt != "" || cfg.SystemPromptFile != "" {
		return nil, fmt.Errorf("freebuff: private additive standing instructions require native proof")
	}
	if !filepath.IsAbs(cfg.WorkspacePath) || strings.ContainsAny(cfg.WorkspacePath, "\x00\r\n") {
		return nil, fmt.Errorf("freebuff: workspace must be an absolute directory path without control delimiters")
	}
	info, err := os.Stat(cfg.WorkspacePath)
	if err != nil {
		return nil, fmt.Errorf("freebuff: workspace unavailable: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("freebuff: workspace must be a directory")
	}
	return []string{"freebuff", "--cwd", cfg.WorkspacePath}, nil
}

// GetPromptDeliveryStrategy keeps user task text out of native argv.
func (p *Plugin) GetPromptDeliveryStrategy(ctx context.Context, _ ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// A readiness detector must be qualified before public launch is enabled.
	return ports.PromptDeliveryAfterStart, nil
}

// GetAgentHooks refuses unqualified standing instruction delivery.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if cfg.SystemPrompt != "" || cfg.SystemPromptFile != "" {
		return ErrQualificationRequired
	}
	return nil
}

// GetRestoreCommand hard-errors for every state: native missing-ID fallback can
// select latest, and visible transcripts alone do not prove model context.
func (p *Plugin) GetRestoreCommand(ctx context.Context, _ ports.RestoreConfig) ([]string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	return nil, false, ErrRestoreUnsupported
}
