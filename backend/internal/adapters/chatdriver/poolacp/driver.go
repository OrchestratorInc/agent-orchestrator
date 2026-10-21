// Package poolacp binds the user's pinned native Pool ACP agent to AO Chat.
package poolacp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/nativeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// New uses Pool's own tools, permissions and durable session history.
func New(plugin nativeacp.Plugin, log *slog.Logger) ports.ChatDriver {
	return nativeacp.New(plugin, nativeacp.Config{
		Harness: domain.HarnessPool, Preflight: preflight, Configure: configure,
		Capabilities:       ports.ChatCapabilities{ports.ChatCapabilityImages: false, ports.ChatCapabilityEmbeddedContext: false, ports.ChatCapabilityResourceLinks: false, ports.ChatCapabilityElicitation: false, ports.ChatCapabilityNestedAgents: false, ports.ChatCapabilityTerminalOutput: false},
		ValidateInitialize: validateInitialize, ValidateSessionReady: validateSessionReady,
		SessionMode: sessionMode, SessionOptions: sessionOptions, ValidateTurnSettings: validateTurnSettings,
	}, log)
}

func validateInitialize(init acpsdk.InitializeResponse) error {
	if init.AgentInfo == nil || init.AgentInfo.Name != "pool-acp" || init.AgentInfo.Version != "1.0.16" || !init.AgentCapabilities.LoadSession {
		return fmt.Errorf("pool Chat requires the qualified native pool-acp 1.0.16 with session/load")
	}
	return nil
}

func hookDirectory(cfg acpdriver.LaunchConfig) (string, error) {
	if !filepath.IsAbs(cfg.DataDir) || cfg.SessionID == "" || strings.ContainsAny(string(cfg.SessionID), "/\\") {
		return "", fmt.Errorf("pool instructions require an absolute AO data directory and session identity")
	}
	return filepath.Join(cfg.DataDir, "prompts", string(cfg.SessionID), "pool"), nil
}

func configure(ctx context.Context, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
	if err := preflight(ctx); err != nil {
		return nil, nil, err
	}
	if err := validateConfiguration(cfg.Env); err != nil {
		return nil, nil, err
	}
	if err := validateTurnSettings(cfg.Permissions, ports.ChatTurnSettings{Approval: cfg.Permissions}); err != nil {
		return nil, nil, err
	}
	dir, err := hookDirectory(cfg)
	if err != nil {
		return nil, nil, err
	}
	helperPath := filepath.Join(dir, "standing.sh")
	settings, helper, err := BuildStandingHook(cfg.SystemPrompt, helperPath)
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, err
	}
	witness := filepath.Join(dir, "started.json")
	if err := os.Remove(witness); err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	// Record the native event before emitting the additive context. No project
	// settings, AGENTS.md, user hooks or default system prompt are replaced.
	helper = []byte(strings.Replace(string(helper), "#!/bin/sh\n", "#!/bin/sh\numask 077\ncat > "+quote(witness)+" || exit 1\n", 1))
	for name, content := range map[string][]byte{"standing.sh": helper, "settings.yaml": settings} {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o600); err != nil {
			return nil, nil, err
		}
	}
	return []string{"acp", "--settings", filepath.Join(dir, "settings.yaml")}, nil, nil
}

func validateSessionReady(ctx context.Context, cfg acpdriver.LaunchConfig, nativeID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := hookDirectory(cfg)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(dir, "started.json"))
	if err != nil {
		return fmt.Errorf("pool standing hook did not run: %w", err)
	}
	var event struct {
		Event   string `json:"hook_event_name"`
		Session string `json:"session_id"`
		Cwd     string `json:"cwd"`
	}
	if err := json.Unmarshal(data, &event); err != nil {
		return fmt.Errorf("pool standing hook witness: %w", err)
	}
	if event.Event != "SessionStart" || event.Session != nativeID || filepath.Clean(event.Cwd) != filepath.Clean(cfg.WorkspacePath) {
		return fmt.Errorf("pool standing hook witness does not match the initialized native session/workspace")
	}
	return nil
}

func sessionMode(mode ports.PermissionMode) string {
	switch ports.NormalizePermissionMode(mode) {
	case ports.PermissionModeDefault:
		return "default"
	case ports.PermissionModeAcceptEdits:
		return "accept-edits"
	case ports.PermissionModeBypassPermissions:
		return "always-allow"
	default:
		return ""
	}
}
func validateTurnSettings(_ ports.PermissionMode, settings ports.ChatTurnSettings) error {
	if sessionMode(settings.Approval) == "" {
		return fmt.Errorf("pool Chat does not support approval mode %q", settings.Approval)
	}
	return nil
}
func sessionOptions(settings ports.ChatTurnSettings) []acpdriver.SessionOption {
	if model := strings.TrimSpace(settings.Model); model != "" {
		return []acpdriver.SessionOption{{ID: "model", Value: model}}
	}
	return nil
}
