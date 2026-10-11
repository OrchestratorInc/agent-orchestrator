// Package dockeragentacp binds the user's Docker Agent config to native ACP.
package dockeragentacp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/dockeragent"
	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/nativeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	acpsdk "github.com/coder/acp-go-sdk"
)

// New uses AO's detached ACP host, without granting client filesystem/terminal
// execution or substituting the provider's configured native tools.
func New(plugin nativeacp.Plugin, log *slog.Logger) ports.ChatDriver {
	return nativeacp.New(plugin, nativeacp.Config{
		Harness:              domain.HarnessDockerAgent,
		Configure:            configure,
		ValidateInitialize:   validateInitialize,
		SessionMode:          sessionMode,
		SessionOptions:       sessionOptions,
		ValidateTurnSettings: validateTurnSettings,
	}, log)
}

func validateInitialize(init acpsdk.InitializeResponse) error {
	// Admission is deliberately pinned to the released distribution qualified by
	// this binding; later versions need native lifecycle qualification first.
	if init.AgentInfo == nil || init.AgentInfo.Name != "docker agent" || init.AgentInfo.Version != "v1.150.0" {
		return fmt.Errorf("Docker Agent native ACP requires tested v1.150.0")
	}
	if !init.AgentCapabilities.LoadSession {
		return fmt.Errorf("Docker Agent must support session/load")
	}
	return nil
}

func configure(ctx context.Context, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if runtime.GOOS == "windows" {
		return nil, nil, fmt.Errorf("Docker Agent private hook binding currently supports Unix only")
	}
	config := os.Getenv(dockeragent.ConfigEnv)
	if value, ok := cfg.Env[dockeragent.ConfigEnv]; ok {
		config = value
	}
	config = strings.TrimSpace(config)
	if !filepath.IsAbs(config) {
		return nil, nil, fmt.Errorf("%s must name an absolute existing Docker Agent YAML config", dockeragent.ConfigEnv)
	}
	info, err := os.Stat(config)
	if err != nil {
		return nil, nil, fmt.Errorf("Docker Agent configuration: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("Docker Agent configuration must be a regular file")
	}
	if !filepath.IsAbs(cfg.DataDir) || cfg.SessionID == "" {
		return nil, nil, fmt.Errorf("Docker Agent requires an absolute AO data directory and session ID")
	}
	// Stable across runtime generations; scope separates independent conversations
	// attached to the same AO session. Workspace matching is enforced by native load.
	owner := sha256.Sum256([]byte(string(cfg.SessionID) + "\x00" + cfg.ProviderScopeID))
	dir := filepath.Join(cfg.DataDir, "agent-runtime", "docker-agent", fmt.Sprintf("%x", owner))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, err
	}
	contextBytes, err := json.Marshal(map[string]any{"hook_specific_output": map[string]string{"additional_context": cfg.SystemPrompt + "\n\nAO session working directory: " + cfg.WorkspacePath + ". Use this directory for workspace tools and task files; do not use the repository source checkout."}})
	if err != nil {
		return nil, nil, err
	}
	// Reject overlarge context instead of accepting native bounded-hook truncation.
	if len(contextBytes) > 32*1024 {
		return nil, nil, fmt.Errorf("Docker Agent private context exceeds 32 KiB")
	}
	contextPath := filepath.Join(dir, "standing-context.json")
	// Atomic replacement lets a reconnect refresh launch context without leaving
	// a partial hook response. No workspace/config/AGENTS files are modified.
	temp, err := os.CreateTemp(dir, ".standing-context-*")
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = os.Remove(temp.Name()) }()
	if _, err := temp.Write(contextBytes); err != nil {
		_ = temp.Close()
		return nil, nil, err
	}
	if err := temp.Close(); err != nil {
		return nil, nil, err
	}
	if err := os.Rename(temp.Name(), contextPath); err != nil {
		return nil, nil, err
	}
	hook := "cat -- " + shellQuote(contextPath)
	args := []string{"--data-dir", dir, "--cache-dir", filepath.Join(dir, "cache"), "serve", "acp", config, "--session-db", filepath.Join(dir, "session.db"), "--working-dir", cfg.WorkspacePath, "--hook-session-start", hook}
	return args, map[string]string{"TELEMETRY_ENABLED": "false"}, nil
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

func sessionMode(mode ports.PermissionMode) string {
	switch ports.NormalizePermissionMode(mode) {
	case ports.PermissionModeBypassPermissions:
		return "autonomous"
	case ports.PermissionModeAuto:
		return "balanced"
	default:
		return "default"
	}
}

func sessionOptions(settings ports.ChatTurnSettings) []acpdriver.SessionOption {
	if model := strings.TrimSpace(settings.Model); model != "" {
		return []acpdriver.SessionOption{{ID: "model", Value: model}}
	}
	return nil
}

func validateTurnSettings(_ ports.PermissionMode, settings ports.ChatTurnSettings) error {
	if settings.Effort != "" {
		return fmt.Errorf("%w: Docker Agent effort must be configured natively", ports.ErrChatCapabilityUnavailable)
	}
	// Docker Agent's filesystem annotations do not distinguish read and edit
	// requests accurately enough to promise AO's accept-edits policy.
	if ports.NormalizePermissionMode(settings.Approval) == ports.PermissionModeAcceptEdits {
		return fmt.Errorf("%w: Docker Agent does not support AO accept-edits", ports.ErrChatCapabilityUnavailable)
	}
	model := strings.TrimSpace(settings.Model)
	if model != "" && model != "default" && !strings.HasPrefix(model, "model:") {
		return fmt.Errorf("%w: select a Docker Agent configured model option", ports.ErrChatConfigOptionInvalid)
	}
	return nil
}
