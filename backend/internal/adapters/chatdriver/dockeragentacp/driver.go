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
	"strconv"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/dockeragent"
	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/nativeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// New uses AO's detached ACP host, without granting client filesystem/terminal
// execution or substituting the provider's configured native tools.
func New(plugin nativeacp.Plugin, log *slog.Logger) ports.ChatDriver {
	return nativeacp.New(plugin, nativeacp.Config{
		Harness:              domain.HarnessDockerAgent,
		Preflight:            preflight,
		ValidateEnvironment:  validateEnvironment,
		Configure:            configure,
		ValidateInitialize:   validateInitialize,
		SessionMode:          sessionMode,
		SessionOptions:       sessionOptions,
		ValidateTurnSettings: validateTurnSettings,
	}, log)
}

// minimumVersion is the first release qualified with this binding. Newer
// releases are admitted so AO can install whatever Docker publishes.
var minimumVersion = [3]int{1, 150, 0}

func validateInitialize(init acpsdk.InitializeResponse) error {
	if init.AgentInfo == nil || init.AgentInfo.Name != "docker agent" || !atLeastMinimum(init.AgentInfo.Version) {
		return fmt.Errorf("docker agent native ACP requires v1.150.0 or newer")
	}
	if !init.AgentCapabilities.LoadSession {
		return fmt.Errorf("docker agent must support session/load")
	}
	return nil
}

// atLeastMinimum reports whether a "vMAJOR.MINOR.PATCH" version (prerelease
// or build suffix ignored) is minimumVersion or newer.
func atLeastMinimum(version string) bool {
	core, _, _ := strings.Cut(strings.TrimPrefix(strings.TrimSpace(version), "v"), "-")
	core, _, _ = strings.Cut(core, "+")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return false
	}
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return false
		}
		if n != minimumVersion[i] {
			return n > minimumVersion[i]
		}
	}
	return true
}

func preflight(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return fmt.Errorf("docker agent private hook binding currently supports Unix only")
	}
	return nil
}

func validateEnvironment(ctx context.Context, env map[string]string) error {
	_, _, err := nativeConfig(ctx, env)
	if err != nil {
		return fmt.Errorf("%w: %w", ports.ErrChatDriverUnavailable, err)
	}
	return nil
}

// nativeConfig returns the user's selected config, or "" and AO's rendered
// default when none is selected; the default needs a provider key it can use.
func nativeConfig(ctx context.Context, env map[string]string) (string, []byte, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	config := os.Getenv(dockeragent.ConfigEnv)
	if value, ok := env[dockeragent.ConfigEnv]; ok {
		config = value
	}
	config = strings.TrimSpace(config)
	if config == "" {
		defaultConfig, err := dockeragent.DefaultConfig(env)
		if err != nil {
			return "", nil, err
		}
		return "", defaultConfig, nil
	}
	if !filepath.IsAbs(config) {
		return "", nil, fmt.Errorf("%s must name an absolute existing Docker Agent YAML config", dockeragent.ConfigEnv)
	}
	info, err := os.Stat(config)
	if err != nil {
		return "", nil, fmt.Errorf("docker agent configuration: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", nil, fmt.Errorf("docker agent configuration must be a regular file")
	}
	return config, nil, nil
}

func configure(ctx context.Context, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
	if err := preflight(ctx); err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ports.ErrChatDriverUnavailable, err)
	}
	config, defaultConfig, err := nativeConfig(ctx, cfg.Env)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ports.ErrChatDriverUnavailable, err)
	}
	if !filepath.IsAbs(cfg.DataDir) || cfg.SessionID == "" {
		return nil, nil, fmt.Errorf("docker agent requires an absolute AO data directory and session ID")
	}
	// Stable across runtime generations; scope separates independent conversations
	// attached to the same AO session. Workspace matching is enforced by native load.
	owner := sha256.Sum256([]byte(string(cfg.SessionID) + "\x00" + cfg.ProviderScopeID))
	dir := filepath.Join(cfg.DataDir, "agent-runtime", "docker-agent", fmt.Sprintf("%x", owner))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, err
	}
	if config == "" {
		config = filepath.Join(dir, "agent.yaml")
		if err := os.WriteFile(config, defaultConfig, 0o600); err != nil {
			return nil, nil, err
		}
	}
	contextBytes, err := json.Marshal(map[string]any{"hook_specific_output": map[string]string{"additional_context": cfg.SystemPrompt + "\n\nAO session working directory: " + cfg.WorkspacePath + ". Use this directory for workspace tools and task files; do not use the repository source checkout."}})
	if err != nil {
		return nil, nil, err
	}
	// Reject overlarge context instead of accepting native bounded-hook truncation.
	if len(contextBytes) > 32*1024 {
		return nil, nil, fmt.Errorf("docker agent private context exceeds 32 KiB")
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
	env := dockeragent.ProviderEnv(cfg.Env)
	env["TELEMETRY_ENABLED"] = "false"
	return args, env, nil
}

// DiscoverConfigOptions lists the model choices Docker Agent advertises for
// the effective config, launching it against a throwaway runtime directory
// under dataDir that is removed afterwards.
func DiscoverConfigOptions(ctx context.Context, binary, workingDir, dataDir string, env map[string]string, log *slog.Logger) ([]ports.ChatConfigOption, error) {
	if err := preflight(ctx); err != nil {
		return nil, err
	}
	config, defaultConfig, err := nativeConfig(ctx, env)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(binary) == "" || !filepath.IsAbs(dataDir) {
		return nil, fmt.Errorf("docker agent discovery requires an installed binary and an absolute AO data directory")
	}
	root := filepath.Join(dataDir, "agent-runtime", "docker-agent")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(root, "discovery-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if config == "" {
		config = filepath.Join(dir, "agent.yaml")
		if err := os.WriteFile(config, defaultConfig, 0o600); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(workingDir) == "" {
		workingDir = dir
	}
	launchEnv := dockeragent.ProviderEnv(env)
	for key, value := range env {
		if _, aliased := launchEnv[key]; !aliased {
			launchEnv[key] = value
		}
	}
	launchEnv["TELEMETRY_ENABLED"] = "false"
	return acpdriver.DiscoverConfigOptions(ctx, acpdriver.Launch{
		Command: binary,
		Args:    []string{"--data-dir", dir, "--cache-dir", filepath.Join(dir, "cache"), "serve", "acp", config, "--session-db", filepath.Join(dir, "session.db"), "--working-dir", workingDir},
		Env:     launchEnv,
	}, workingDir, log)
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
	// "default" is the catalog placeholder, not a configured model name, so
	// the config's own default model applies.
	if model := strings.TrimSpace(settings.Model); model != "" && model != "default" {
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
