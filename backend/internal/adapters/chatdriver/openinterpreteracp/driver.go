// Package openinterpreteracp contains an UNREGISTERED candidate for the released
// Rust Open Interpreter ACP server. It must not be added to the production Chat
// registry until invocation-scoped private hooks, safe option exposure, and executing-child cancellation
// are qualified. In 0.0.56, ACP drops CLI config overrides, while native resume
// retains the original developer instructions instead of reading their update.
package openinterpreteracp

import (
	"context"
	"fmt"
	"log/slog"

	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/nativeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Plugin reuses the TUI plugin's binary/auth discovery and read-only native
// history validation. No second history format or provider profile is invented.
type Plugin interface {
	nativeacp.Plugin
	ValidateNativeRestore(context.Context, map[string]string, string, string) error
}

type driver struct {
	ports.ChatDriver
	plugin Plugin
}

// New builds the candidate only. The production registry deliberately excludes
// it. Nonempty standing instructions fail admission rather than being silently
// dropped or placed in the user's visible task.
func New(plugin Plugin, log *slog.Logger) ports.ChatDriver {
	return &driver{plugin: plugin, ChatDriver: nativeacp.New(plugin, nativeacp.Config{
		Harness:                  domain.HarnessOpenInterpreter,
		Capabilities:             ports.ChatCapabilities{ports.ChatCapabilityInterrupt: false},
		Configure:                configure,
		ValidateInitialize:       validateInitialize,
		SessionMode:              sessionMode,
		SessionOptions:           sessionOptions,
		RequireAdvertisedOptions: true,
		ValidateTurnSettings:     validateTurnSettings,
	}, log)}
}

func configure(ctx context.Context, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if cfg.SystemPrompt != "" {
		return nil, nil, fmt.Errorf("%w: Open Interpreter 0.0.56 ACP ignores CLI instruction overrides; renewed private instructions are not qualified", ports.ErrChatDriverIncompatible)
	}
	if err := validateTurnSettings(cfg.Permissions, ports.ChatTurnSettings{Approval: cfg.Permissions}); err != nil {
		return nil, nil, err
	}
	// Model and effort use the native advertised ACP setters, never ignored -c.
	return []string{"acp"}, nil, nil
}

func validateInitialize(init acpsdk.InitializeResponse) error {
	info := init.AgentInfo
	if info == nil || info.Name != "codex-acp" || info.Version != "0.0.56" || info.Title == nil || *info.Title != "Open Interpreter" {
		return fmt.Errorf("%w: expected Open Interpreter codex-acp 0.0.56", ports.ErrChatDriverIncompatible)
	}
	return nil
}

func sessionMode(mode ports.PermissionMode) string {
	if mode == ports.PermissionModeBypassPermissions {
		return "full-access"
	}
	return "workspace-write"
}

func validateTurnSettings(_ ports.PermissionMode, s ports.ChatTurnSettings) error {
	switch s.Approval {
	case "", ports.PermissionModeDefault, ports.PermissionModeBypassPermissions:
	default:
		return fmt.Errorf("%w: Open Interpreter ACP has no exact mapping for %q", ports.ErrChatPermissionModeUnsupported, s.Approval)
	}
	switch s.Effort {
	case "", "default", "minimal", "low", "medium", "high":
	default:
		return fmt.Errorf("open interpreter ACP: unsupported reasoning effort %q", s.Effort)
	}
	return nil
}

func sessionOptions(s ports.ChatTurnSettings) []acpdriver.SessionOption {
	var options []acpdriver.SessionOption
	if s.Model != "" {
		options = append(options, acpdriver.SessionOption{ID: "model", Value: s.Model})
	}
	if s.Effort != "" {
		options = append(options, acpdriver.SessionOption{ID: "reasoning_effort", Value: s.Effort})
	}
	return options
}

// Start validates even before adopting a surviving host, which skips Configure.
func (d *driver) Start(ctx context.Context, cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
	if err := admit(ctx, cfg.SystemPrompt, cfg.ReadOnly); err != nil {
		return nil, err
	}
	return d.ChatDriver.Start(ctx, cfg)
}

func admit(ctx context.Context, prompt string, readOnly bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if readOnly {
		return fmt.Errorf("%w: read-only Open Interpreter Chat is not qualified", ports.ErrChatPermissionModeUnsupported)
	}
	_, _, err := configure(ctx, acpdriver.LaunchConfig{SystemPrompt: prompt})
	return err
}

func (d *driver) Resume(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	if cfg.ReconnectOnly {
		return d.Reconnect(ctx, cfg)
	}
	if err := admit(ctx, cfg.SystemPrompt, cfg.ReadOnly); err != nil {
		return nil, err
	}
	if cfg.PrepareEnv != nil {
		prepare := cfg.PrepareEnv
		// The callback runs only for a replacement process. Check precisely its
		// environment before nativeacp launches, not the stale pre-rotation profile.
		cfg.PrepareEnv = func(ctx context.Context) (map[string]string, error) {
			env, err := prepare(ctx)
			if err != nil {
				return nil, err
			}
			if err := d.plugin.ValidateNativeRestore(ctx, env, cfg.WorkspacePath, cfg.ProviderConversationID); err != nil {
				return nil, fmt.Errorf("%w: %w", ports.ErrChatResumeFailed, err)
			}
			return env, nil
		}
	} else if err := d.plugin.ValidateNativeRestore(ctx, cfg.Env, cfg.WorkspacePath, cfg.ProviderConversationID); err != nil {
		return nil, fmt.Errorf("%w: %w", ports.ErrChatResumeFailed, err)
	}
	// Even a caller's fresh-if-missing hint cannot substitute another history.
	cfg.FreshIfMissing = false
	return d.ChatDriver.Resume(ctx, cfg)
}

// Reconnect attaches only to a surviving persistent host. No native-history
// guard or launch credential rotation belongs on this path.
func (d *driver) Reconnect(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	if err := admit(ctx, cfg.SystemPrompt, cfg.ReadOnly); err != nil {
		return nil, err
	}
	reconnect, ok := d.ChatDriver.(ports.ChatDriverReconnector)
	if !ok {
		return nil, ports.ErrChatHostNotRunning
	}
	cfg.ReconnectOnly = true
	return reconnect.Reconnect(ctx, cfg)
}

var _ ports.ChatDriverReconnector = (*driver)(nil)
