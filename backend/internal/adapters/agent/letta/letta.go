// Package letta integrates Letta Code's interactive CLI (v0.34.9).
// AO instructions arrive through a quiet UserPromptSubmit hook, preserving
// Letta's own system prompt and project instructions on both start and restore.
package letta

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// InspectedVersion and InspectedCommit identify the released source contract.
const InspectedVersion = "0.34.9"
const InspectedCommit = "cbf9026030c74e60432c246aec2bb0ec08fd49bc"

var binarySpec = binaryutil.BinarySpec{
	Label: "letta", Names: []string{"letta"},
	WinNames:      []string{"letta.cmd", "letta.exe", "letta"},
	UnixPaths:     []string{"/usr/local/bin/letta", "/opt/homebrew/bin/letta"},
	UnixHomePaths: binaryutil.NodeManagedUnixHomePaths("letta"), NodeManaged: true,
	WinPaths: []binaryutil.WinPath{{Base: binaryutil.WinAppData, Parts: []string{"npm", "letta.cmd"}}},
}

// Plugin implements terminal sessions, native restore and local hook ownership.
type Plugin struct {
	agentbase.Base
	binaryMu       sync.Mutex
	resolvedBinary string
}

// New constructs the Letta Code adapter.
func New() *Plugin { return &Plugin{} }

var _ ports.Agent = (*Plugin)(nil)

// Manifest returns the user-selectable harness identity.
func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{ID: "letta-code", Name: "Letta Code", Description: "Run Letta Code terminal sessions.", Version: "0.0.1", Capabilities: []adapters.Capability{adapters.CapabilityAgent}}
}

// GetConfigSpec exposes the native model selector.
func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx, "Model handle or catalog ID passed to letta --model.")
}

func (p *Plugin) command(ctx context.Context, config ports.AgentConfig, mode ports.PermissionMode, allowed, denied []string) ([]string, error) {
	if len(allowed) != 0 || len(denied) != 0 {
		return nil, fmt.Errorf("letta-code: tool restrictions are not supported by this adapter")
	}
	if strings.TrimSpace(config.Effort) != "" {
		return nil, fmt.Errorf("letta-code: reasoning effort overrides are not supported")
	}
	var permission string
	switch mode {
	case "", ports.PermissionModeDefault:
		permission = "standard"
	case ports.PermissionModeAcceptEdits:
		permission = "acceptEdits"
	case ports.PermissionModeBypassPermissions:
		permission = "unrestricted"
	default:
		return nil, fmt.Errorf("letta-code: unsupported permission mode %q", mode)
	}
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return nil, err
	}
	cmd := []string{binary, "--permission-mode", permission}
	agentbase.AppendModelFlag(&cmd, config, "--model")
	return cmd, nil
}

// GetLaunchCommand creates a dedicated agent and a real conversation ID.
// A bare Letta launch reuses project history; --new-agent alone uses the
// non-unique "default" conversation sentinel. Both flags are necessary.
func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	cmd, err := p.command(ctx, cfg.Config, cfg.Permissions, cfg.AllowedTools, cfg.DisallowedTools)
	if err != nil {
		return nil, err
	}
	return append(cmd, "--new-agent", "--new"), nil
}

// GetPromptDeliveryStrategy avoids Letta's positional subcommand parser.
func (p *Plugin) GetPromptDeliveryStrategy(ctx context.Context, _ ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	return ports.PromptDeliveryAfterStart, ctx.Err()
}

// PromptReadinessHints requires a rendered empty composer, never the banner.
// These are the rotating composer hints in released InputRich.tsx. Disabling
// them upstream fails safely rather than typing into an auth/startup dialog.
func (p *Plugin) PromptReadinessHints(ctx context.Context, _ ports.LaunchConfig) (ports.PromptReadinessHints, error) {
	return ports.PromptReadinessHints{RequireReady: true, Patterns: []string{
		`(shift+tab to cycle)`, `Try "help me understand this codebase"`, `Try "help me organize my desktop"`,
		`Try "debug this error"`, `Try "explain what this function does"`, `Try "review this pull request"`,
	}, PollInterval: 200 * time.Millisecond, Timeout: 45 * time.Second, Lines: 8}, ctx.Err()
}

var conversationIDPattern = regexp.MustCompile(`^conv-[a-zA-Z0-9-]{1,200}$`)

// ValidConversationID excludes default, latest, whitespace and option values.
func ValidConversationID(id string) bool { return conversationIDPattern.MatchString(id) }

// GetRestoreCommand selects exactly the captured native conversation. A missing
// ID is an error: silently creating a new conversation would lose continuity.
func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	id := cfg.Session.Metadata[ports.MetadataKeyAgentSessionID]
	if !ValidConversationID(id) {
		return nil, false, fmt.Errorf("letta-code: native conversation ID is missing or invalid")
	}
	cmd, err := p.command(ctx, cfg.Config, cfg.Permissions, cfg.AllowedTools, cfg.DisallowedTools)
	if err != nil {
		return nil, false, err
	}
	return append(cmd, "--conversation", id), true, nil
}

// SessionInfo returns only a real conversation identity captured by hooks.
func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(session)
	if !ValidConversationID(info.AgentSessionID) {
		return ports.SessionInfo{}, false, nil
	}
	return info, ok, nil
}

// ResolveBinary resolves npm's native platform shim without executing it.
func (p *Plugin) ResolveBinary(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()
	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}
	binary, err := binaryutil.ResolveBinary(ctx, binarySpec)
	if err != nil {
		return "", err
	}
	p.resolvedBinary = binary
	return binary, nil
}

// ExitDetectionMode uses AO's process supervisor, including abrupt exits.
func (p *Plugin) ExitDetectionMode() ports.AgentExitDetectionMode {
	return ports.AgentExitDetectionSupervisor
}

// EmitsSubmitActivity reports Letta's native prompt-submit hook.
func (p *Plugin) EmitsSubmitActivity() bool { return true }

// InterruptInput cancels a Letta turn; Ctrl+C clears a draft or exits instead.
func (p *Plugin) InterruptInput() string { return "\x1b" }

// EmitsSemanticMessageAcceptance reports opaque coordination IDs from native
// UserPromptSubmit. It does not claim transcript replay or Chat support.
func (p *Plugin) EmitsSemanticMessageAcceptance() bool { return true }
