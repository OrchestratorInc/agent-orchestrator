// Package codebuddyacp contains an UNREGISTERED candidate for official CodeBuddy
// Code 2.164.0. Authentication, exact native history, lifecycle and actual AO Chat
// UI qualification remain prerequisites to registration. This package does not
// establish supported Chat capability merely by constructing a shared driver.
package codebuddyacp

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	acpsdk "github.com/coder/acp-go-sdk"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/nativeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const candidateVersion = "2.164.0"

// Plugin shares the installed binary, auth status and native history guard.
type Plugin interface {
	nativeacp.Plugin
	ValidateChatHistory(context.Context, ports.ChatResumeConfig) error
}

// NewCandidate uses the installed executable and shared detached ACP transport.
// It stays unregistered until real native account/lifecycle qualification passes.
func NewCandidate(plugin Plugin, log *slog.Logger) ports.ChatDriver {
	return &driver{ChatDriver: nativeacp.New(plugin, nativeacp.Config{
		Harness: domain.HarnessCodeBuddy,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityImages:          false,
			ports.ChatCapabilityEmbeddedContext: false,
			ports.ChatCapabilitySteer:           false,
			ports.ChatCapabilityCompaction:      false,
			ports.ChatCapabilityRollback:        false,
		},
		Configure: func(ctx context.Context, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
			if err := validateOwnedProfile(cfg); err != nil {
				return nil, nil, err
			}
			return configure(ctx, cfg)
		},
		VersionProbe:         versionProbe,
		ValidateTurnSettings: validateTurnSettings,
		ValidateConfigOption: func(string, ports.ChatConfigOptionValue) error {
			return fmt.Errorf("%w: CodeBuddy ACP config changes are not qualified", ports.ErrChatConfigOptionInvalid)
		},
		ValidateInitialize:           validateInitialize,
		DecodeProviderConversationID: decodeNativeID,
	}, log), plugin: plugin}
}

type driver struct {
	ports.ChatDriver
	plugin Plugin
}

func (d *driver) Resume(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	if _, err := decodeNativeID(cfg.ProviderConversationID); err != nil {
		return nil, fmt.Errorf("%w: %w", ports.ErrChatResumeFailed, err)
	}
	// A surviving host owns its journal even before the first assistant reply.
	// Only proven host absence permits replacement and disk-history validation.
	if conv, err := d.Reconnect(ctx, cfg); !errors.Is(err, ports.ErrChatHostNotRunning) || cfg.ReconnectOnly {
		return conv, err
	}
	if err := validateOwnedProfile(acpdriver.LaunchConfig{SessionID: cfg.SessionID, DataDir: cfg.DataDir, Env: cfg.Env}); err != nil {
		return nil, fmt.Errorf("%w: %w", ports.ErrChatResumeFailed, err)
	}
	if err := d.plugin.ValidateChatHistory(ctx, cfg); err != nil {
		return nil, fmt.Errorf("%w: CodeBuddy history preflight: %w", ports.ErrChatResumeFailed, err)
	}
	cfg.FreshIfMissing = false
	return d.ChatDriver.Resume(ctx, cfg)
}

func (d *driver) Reconnect(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	if _, err := decodeNativeID(cfg.ProviderConversationID); err != nil {
		return nil, fmt.Errorf("%w: %w", ports.ErrChatResumeFailed, err)
	}
	cfg.FreshIfMissing = false
	reconnector, ok := d.ChatDriver.(ports.ChatDriverReconnector)
	if !ok {
		return nil, fmt.Errorf("%w: CodeBuddy reconnect is unavailable", ports.ErrChatResumeFailed)
	}
	return reconnector.Reconnect(ctx, cfg)
}

var nativeIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_:-]{0,255}$`)

func decodeNativeID(id string) (string, error) {
	if !nativeIDPattern.MatchString(id) || id == "latest" || id == "continue" {
		return "", errors.New("CodeBuddy restore requires an exact native conversation ID")
	}
	return id, nil
}

func validateInitialize(init acpsdk.InitializeResponse) error {
	// Stock 2.164.0 omits AgentInfo. Version is checked against the exact launch
	// binary; do not invent an advertised identity that native never sends.
	if init.ProtocolVersion != acpsdk.ProtocolVersionNumber || !init.AgentCapabilities.LoadSession {
		return errors.New("CodeBuddy ACP candidate requires ACP v1 with session/load")
	}
	return nil
}

// Candidate qualification must supply a private native profile explicitly.
// Refuse implicit native-home writes or adopting another session's credentials,
// preferences/history. Provisioning that profile is outside shipping support.
const profileMarker = "agent-orchestrator: private codebuddy ACP profile v1\n"

func validateOwnedProfile(cfg acpdriver.LaunchConfig) error {
	if cfg.SessionID == "" || filepath.Base(string(cfg.SessionID)) != string(cfg.SessionID) || strings.ContainsAny(string(cfg.SessionID), `/\\`) || !filepath.IsAbs(cfg.DataDir) {
		return fmt.Errorf("%w: CodeBuddy candidate requires an isolated AO session profile", ports.ErrChatDriverUnavailable)
	}
	root, err := filepath.EvalSymlinks(cfg.DataDir)
	if err != nil {
		return err
	}
	expected := filepath.Join(root, "agents", "codebuddy", "chat", string(cfg.SessionID))
	profile := cfg.Env["CODEBUDDY_CONFIG_DIR"]
	if !filepath.IsAbs(profile) {
		return fmt.Errorf("%w: CodeBuddy candidate requires an explicit private CODEBUDDY_CONFIG_DIR", ports.ErrChatDriverUnavailable)
	}
	canonical, err := filepath.EvalSymlinks(profile)
	if err != nil || canonical != expected {
		return fmt.Errorf("%w: CodeBuddy native profile is not owned by this AO session", ports.ErrChatDriverUnavailable)
	}
	info, err := os.Lstat(profile)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: CodeBuddy native profile must be a private directory", ports.ErrChatDriverUnavailable)
	}
	marker := filepath.Join(profile, ".ao-managed")
	info, err = os.Lstat(marker)
	if err != nil || !info.Mode().IsRegular() || info.Size() != int64(len(profileMarker)) {
		return fmt.Errorf("%w: CodeBuddy native profile ownership marker missing", ports.ErrChatDriverUnavailable)
	}
	body, err := os.ReadFile(marker)
	if err != nil || string(body) != profileMarker {
		return fmt.Errorf("%w: CodeBuddy native profile ownership marker invalid", ports.ErrChatDriverUnavailable)
	}
	return nil
}

func configure(ctx context.Context, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
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
