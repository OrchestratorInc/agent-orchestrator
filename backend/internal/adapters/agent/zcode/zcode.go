// Package zcode implements the ZCode (Z.AI GLM) agent adapter.
//
// ZCode is Z.AI's official coding agent (binary "zcode", official open repo
// github.com/zai-org/ZCode — source for the Agent CLI and runtime, Apache-2.0;
// its interactive CLI currently requires a source build, because desktop
// release bundles omit the TUI). With no arguments it opens an interactive TUI, which is how AO
// launches it; the initial task is delivered through the terminal after the
// TUI is ready because both --prompt and -p/--print run a single headless
// turn and exit, and a bare positional argument is parsed as a subcommand.
//
// Permission handling maps AO's permission modes onto `--mode`
// (build|edit|plan|yolo). The default explicitly selects build so a saved yolo
// configuration cannot broaden AO permissions. Restore resumes a persisted session
// with `--resume <sessionId>`.
//
// AO installs workspace hooks under .zcode/config.json and grants only their
// exact native declaration digests. UserPromptSubmit supplies hidden additive
// standing instructions; lifecycle callbacks capture the native session ID.
// Model selection stays in ZCode's own model.main configuration.
package zcode

import (
	"context"
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var zcodeBinarySpec = binaryutil.BinarySpec{
	Label:     "zcode",
	Names:     []string{"zcode"},
	WinNames:  []string{"zcode.cmd", "zcode.exe", "zcode"},
	UnixPaths: []string{"/usr/local/bin/zcode", "/opt/homebrew/bin/zcode"},
	// ZCode's interactive CLI is built from the official source repository
	// github.com/zai-org/ZCode; there is no official npm package. It is a
	// Node-based binary, so version-manager shims (nvm/Volta/fnm) and the
	// Node-managed home paths remain valid resolution targets for a manually built CLI.
	UnixHomePaths: binaryutil.NodeManagedUnixHomePaths("zcode"),
	NodeManaged:   true,
	WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "zcode.cmd"}},
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "zcode.exe"}},
	},
}

// Plugin is the ZCode agent adapter.
type Plugin struct {
	agentbase.Base
	binaryMu        sync.Mutex
	resolvedBinary  string
	validateRestore func(context.Context, string, string, map[string]string) error
}

// New returns a ready-to-register ZCode adapter.
func New() *Plugin {
	return &Plugin{}
}

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)

// Manifest returns the adapter's static self-description.
func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          "zcode",
		Name:        "ZCode",
		Description: "Run Z.AI ZCode (GLM) worker sessions.",
		Version:     "0.0.1",
		Capabilities: []adapters.Capability{
			adapters.CapabilityAgent,
		},
	}
}

// GetConfigSpec reports ZCode's permission modes. ZCode pins its model in
// its own config (model.main); zcode has no --model launch flag, so AO
// exposes the mode instead of a misleading raw-model field.
func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	if err := ctx.Err(); err != nil {
		return ports.ConfigSpec{}, err
	}
	return ports.ConfigSpec{Fields: []ports.ConfigField{{
		Key:         "mode",
		Type:        ports.ConfigFieldEnum,
		Description: "ZCode permission mode passed to `zcode --mode`.",
		Enum:        []string{"build", "edit", "plan", "yolo"},
	}}}, nil
}

// GetLaunchCommand rejects the stock TUI: it has no supported additive
// workspace-hook channel. Never pass the nonexistent trust flag to a user TUI.
func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	if err := validateStandingInstructions(ctx, cfg.SystemPrompt, cfg.SystemPromptFile); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("zcode: Terminal UI is unsupported; use Chat (native app-server)")
}

// GetPromptDeliveryStrategy reports that AO should inject prompted ZCode tasks
// into the interactive terminal after startup.
func (p *Plugin) GetPromptDeliveryStrategy(ctx context.Context, _ ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return ports.PromptDeliveryAfterStart, nil
}

// PromptReadinessHints requires the current native composer and rejects the
// model-configuration screen, which renders an identical composer underneath.
func (p *Plugin) PromptReadinessHints(ctx context.Context, _ ports.LaunchConfig) (ports.PromptReadinessHints, error) {
	if err := ctx.Err(); err != nil {
		return ports.PromptReadinessHints{
			RequireReady: true}, err
	}
	return ports.PromptReadinessHints{
		RequireReady:    true,
		InitialDelay:    750 * time.Millisecond,
		Patterns:        []string{"Type a prompt", "输入提示词"},
		BlockedPatterns: []string{"No available models.", "没有可用模型"},
		PollInterval:    200 * time.Millisecond,
		Timeout:         10 * time.Second,
		Lines:           80,
	}, nil
}

// GetRestoreCommand rejects terminal restores. Native Chat restore uses the
// dedicated app-server driver and never falls back to a fresh TUI.
func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	if err := validateStandingInstructions(ctx, cfg.SystemPrompt, cfg.SystemPromptFile); err != nil {
		return nil, false, err
	}
	return nil, false, fmt.Errorf("zcode: Terminal UI is unsupported; use Chat (native app-server)")
}

// SessionInfo reads hook-derived metadata under AO's normalized keys ("title",
// "summary", "agentSessionId"). ZCode reports these through the AO-managed workspace hooks.
func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(session)
	return info, ok, nil
}

// ResolveZcodeBinary finds the `zcode` binary (Z.AI ZCode CLI).
func ResolveZcodeBinary(ctx context.Context) (string, error) {
	return binaryutil.ResolveBinary(ctx, zcodeBinarySpec)
}

func (p *Plugin) zcodeBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}

	binary, err := ResolveZcodeBinary(ctx)
	if err != nil {
		return "", err
	}
	p.resolvedBinary = binary
	return binary, nil
}

// appendModeFlags maps AO permission modes onto zcode's --mode values. An
// explicit per-session mode config (build|edit|plan|yolo) wins; otherwise the
// permission mode is mapped — acceptEdits→edit, auto→build (zcode's internal
// "auto" mode is reserved but unimplemented), bypassPermissions→yolo. With neither, build is explicit so a saved yolo setting cannot broaden AO permissions.
var nativeIDPattern = regexp.MustCompile(`^sess_[A-Za-z0-9._-]+$`)

// InterruptInput cancels a running native turn. Ctrl+C only clears the draft
// or arms ZCode's exit confirmation; Escape is its turn-cancellation key.
func (p *Plugin) InterruptInput() string { return "\x1b" }

// UserPromptSubmit precedes other native hook vetoes, so it cannot acknowledge
// coordination delivery. AO leaves semantic acceptance unsupported.

// ExitDetectionMode leaves process exit detection to the runtime supervisor.
func (p *Plugin) ExitDetectionMode() ports.AgentExitDetectionMode {
	return ports.AgentExitDetectionSupervisor
}
