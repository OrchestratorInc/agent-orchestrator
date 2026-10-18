// Package codebuddyacp contains an UNREGISTERED candidate for official CodeBuddy
// Code 2.164.0. Authentication, exact native history, lifecycle and actual AO Chat
// UI qualification remain prerequisites to registration. This package does not
// establish supported Chat capability merely by constructing a shared driver.
package codebuddyacp

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/nativeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const candidateVersion = "2.164.0"

// NewCandidate uses the user's installed executable and shared detached ACP
// transport. It is intentionally absent from registry.Build: the real account
// currently rejects session/new and session/load before history admission.
func NewCandidate(plugin nativeacp.Plugin, log *slog.Logger) ports.ChatDriver {
	return nativeacp.New(plugin, nativeacp.Config{
		Harness:              domain.HarnessCodeBuddy,
		Configure:            configure,
		VersionProbe:         versionProbe,
		ValidateTurnSettings: validateTurnSettings,
	}, log)
}

func configure(_ context.Context, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
	if err := validateTurnSettings(cfg.Permissions, ports.ChatTurnSettings{Model: cfg.Model}); err != nil {
		return nil, nil, err
	}
	// Preserve the native default prompt and independent user/project settings.
	// Instructions never become visible task text. Authenticated provider wire
	// role and actual consumption still require native qualification.
	prompt := cfg.SystemPrompt
	if len(prompt) > 64<<10 || !utf8.ValidString(prompt) || strings.ContainsRune(prompt, 0) {
		return nil, nil, fmt.Errorf("codebuddy ACP: private instructions must be valid UTF-8 without NUL and at most 64 KiB")
	}
	args := []string{"--acp", "--permission-mode", "default"}
	if prompt != "" {
		args = append(args, "--append-system-prompt", prompt)
	}
	// Match the existing TUI adapter's foreground ownership policy; these do not
	// grant tool permissions or replace any native sandbox/security control.
	return args, map[string]string{
		"CODEBUDDY_CODE_DISABLE_BACKGROUND_TASKS": "1",
		"CODEBUDDY_DISABLE_CRON":                  "1",
		"DISABLE_AUTOUPDATER":                     "1",
	}, nil
}

func validateTurnSettings(initial ports.PermissionMode, settings ports.ChatTurnSettings) error {
	if (initial != "" && initial != ports.PermissionModeDefault) ||
		(settings.Approval != "" && settings.Approval != ports.PermissionModeDefault) {
		return fmt.Errorf("%w: CodeBuddy ACP candidate requires default native permissions", ports.ErrChatConfigOptionInvalid)
	}
	// Do not guess a model catalog or silently ignore per-turn settings before an
	// authenticated native session has qualified its advertised setters.
	if settings.Model != "" || settings.Effort != "" {
		return fmt.Errorf("%w: CodeBuddy ACP model and effort selection are not qualified", ports.ErrChatConfigOptionInvalid)
	}
	return nil
}

func versionProbe(ctx context.Context, binary string) error {
	output, err := aoprocess.CommandContext(ctx, binary, "--version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("read CodeBuddy version: %w", err)
	}
	return validateVersionOutput(string(output))
}

func validateVersionOutput(output string) error {
	if strings.TrimSpace(output) != candidateVersion {
		return fmt.Errorf("CodeBuddy ACP candidate requires exact official version %s", candidateVersion)
	}
	return nil
}
