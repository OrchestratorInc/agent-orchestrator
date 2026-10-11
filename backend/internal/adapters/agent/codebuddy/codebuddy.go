// Package codebuddy supervises Tencent CodeBuddy Code's interactive TUI.
// Chat, interface handoff, reviewers and native background tasks are excluded.
package codebuddy

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const maxInstructions = 64 << 10

type Plugin struct {
	agentbase.Base
	mu     sync.Mutex
	binary string
}

func New() *Plugin { return &Plugin{} }

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)

func (*Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{ID: "codebuddy", Name: "CodeBuddy Code", Description: "Tencent CodeBuddy Code foreground TUI sessions.", Version: "0.0.1", Capabilities: []adapters.Capability{adapters.CapabilityAgent}}
}

func (*Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx, "Native --model ID; configure providers in CodeBuddy first.")
}

var binarySpec = binaryutil.BinarySpec{
	Label: "codebuddy", Names: []string{"codebuddy", "cbc"}, WinNames: []string{"codebuddy.cmd", "codebuddy.exe", "cbc.cmd"},
	UnixPaths:     []string{"/usr/local/bin/codebuddy", "/opt/homebrew/bin/codebuddy"},
	UnixHomePaths: binaryutil.NodeManagedUnixHomePaths("codebuddy"), NodeManaged: true,
	WinPaths: []binaryutil.WinPath{{Base: binaryutil.WinAppData, Parts: []string{"npm", "codebuddy.cmd"}}},
}

func (p *Plugin) ResolveBinary(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.binary != "" {
		return p.binary, nil
	}
	binary, err := binaryutil.ResolveBinary(ctx, binarySpec)
	if err == nil {
		p.binary = binary
	}
	return binary, err
}

func (p *Plugin) InvalidateBinaryResolution() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.binary = ""
}

func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	cmd, err := p.command(ctx, cfg.Config, cfg.Permissions, cfg.SystemPrompt, cfg.SystemPromptFile, cfg.AllowedTools, cfg.DisallowedTools)
	if err != nil {
		return nil, err
	}
	if err := validateTask(cfg.Prompt); err != nil {
		return nil, err
	}
	if cfg.Prompt != "" {
		cmd = append(cmd, "--", cfg.Prompt)
	}
	return cmd, nil
}

func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	id := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	// false would let the manager replay the old task as a fresh conversation.
	if err := validateHistory(ctx, cfg.Session.WorkspacePath, id, cfg.Env); err != nil {
		return nil, false, err
	}
	cmd, err := p.command(ctx, cfg.Config, cfg.Permissions, cfg.SystemPrompt, cfg.SystemPromptFile, cfg.AllowedTools, cfg.DisallowedTools)
	if err != nil {
		return nil, false, err
	}
	cmd = append(cmd, "--resume", id)
	if err := validateTask(cfg.Prompt); err != nil {
		return nil, false, err
	}
	if cfg.Prompt != "" {
		cmd = append(cmd, "--", cfg.Prompt)
	}
	return cmd, true, nil
}

func (p *Plugin) command(ctx context.Context, cfg ports.AgentConfig, permissions ports.PermissionMode, inline, file string, allowed, denied []string) ([]string, error) {
	// Only native manual/default permission mode has been qualified. Do not
	// silently substitute a permissive mode or infer auto from a CLI declaration.
	if ports.NormalizePermissionMode(permissions) != ports.PermissionModeDefault {
		return nil, fmt.Errorf("codebuddy: only default native permissions are supported")
	}
	text, err := instructionText(inline, file)
	if err != nil {
		return nil, err
	}
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return nil, err
	}
	cmd := []string{binary}
	agentbase.AppendModelFlag(&cmd, cfg, "--model")
	if text != "" {
		cmd = append(cmd, "--append-system-prompt", text)
	}
	if len(allowed) > 0 {
		cmd = append(cmd, "--allowedTools", strings.Join(allowed, ","))
	}
	if len(denied) > 0 {
		cmd = append(cmd, "--disallowedTools", strings.Join(denied, ","))
	}
	return cmd, nil
}

func instructionText(inline, file string) (string, error) {
	if inline == "" && file != "" {
		f, err := os.Open(file) //nolint:gosec // AO-owned instruction path
		if err != nil {
			return "", fmt.Errorf("codebuddy: read private instructions: %w", err)
		}
		defer f.Close()
		b, err := io.ReadAll(io.LimitReader(f, maxInstructions+1))
		if err != nil {
			return "", fmt.Errorf("codebuddy: read private instructions: %w", err)
		}
		inline = string(b)
	}
	if len(inline) > maxInstructions || !utf8.ValidString(inline) || strings.ContainsRune(inline, 0) {
		return "", fmt.Errorf("codebuddy: private instructions must be valid UTF-8 without NUL and at most 64 KiB")
	}
	return inline, nil
}

func validateTask(task string) error {
	if !utf8.ValidString(task) || strings.ContainsAny(task, "\x00\x1b") {
		return fmt.Errorf("codebuddy: task contains unsupported terminal control data")
	}
	return nil
}

func (*Plugin) AugmentRuntimeLaunchEnv(env map[string]string, _ string, _ domain.SessionID, _ string) {
	// Supported native controls keep Bash/Agent foreground and prevent cron
	// work from outliving the supervised conversation; no native security is disabled.
	env["CODEBUDDY_CODE_DISABLE_BACKGROUND_TASKS"] = "1"
	env["CODEBUDDY_DISABLE_CRON"] = "1"
	env["DISABLE_AUTOUPDATER"] = "1"
}

func (*Plugin) SessionInfo(ctx context.Context, ref ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(ref)
	return info, ok, nil
}

func (*Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	// Environment presence is not a provider authorization round trip. Native
	// login/model configuration remains native-owned; never import another CLI's login.
	for _, k := range []string{"CODEBUDDY_API_KEY", "CODEBUDDY_AUTH_TOKEN"} {
		if strings.TrimSpace(os.Getenv(k)) != "" {
			return ports.AgentAuthStatusConfigured, nil
		}
	}
	return ports.AgentAuthStatusUnknown, nil
}

func (*Plugin) ExitDetectionMode() ports.AgentExitDetectionMode {
	return ports.AgentExitDetectionSupervisor
}
