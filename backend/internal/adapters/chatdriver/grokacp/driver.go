// Package grokacp binds the user's own Grok Build installation to AO's
// reusable ACP Chat transport.
//
// Grok Build ships a first-party ACP agent (`grok agent stdio`), verified
// against v1.0.52 on 2026-10-15:
//
//	protocolVersion 1
//	loadSession      true            -> ChatCapabilityResume
//	promptCapabilities{embeddedContext}
//	mcpCapabilities{http, sse}
//	sessionCapabilities{list, resume, close}
//
// session/new advertises no modes. Its configOptions are `model` and
// `reasoning_effort`, both live through session/set_config_option. Permission
// posture is therefore a launch-time choice, as in other Grok ACP clients.
package grokacp

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/nativeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// New launches `grok agent stdio` from the exact binary resolved by the
// existing Grok agent plugin. Login, models, settings, and updates stay with
// the user's Grok installation.
func New(plugin nativeacp.Plugin, log *slog.Logger) ports.ChatDriver {
	return nativeacp.New(plugin, nativeacp.Config{
		Harness: domain.HarnessGrok,
		Standby: &acpdriver.StandbyConfig{},
		// Grok saves its session after every turn: one stopped by SIGTERM
		// without session/close resumes with its last turn. Its close answer
		// takes 2.5 to 4s and its exit on stdin closing about 2.5s, so waiting
		// for either only slows a switch away from it.
		QuickStop: true,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityModels:        true,
			ports.ChatCapabilityConfigOptions: true,
		},
		Configure:            configure,
		SessionMeta:          sessionMeta,
		SessionOptions:       sessionOptions,
		PermissionPolicy:     permissionPolicy,
		ValidateTurnSettings: validateTurnSettings,
	}, log)
}

// configure builds `grok [global flags] agent [agent flags] stdio`.
//
// `--permission-mode` is a global flag and must precede `agent`;
// `--always-approve` is an agent flag and must follow it. `grok agent` has no
// accept-edits posture (it treats acceptEdits as ask), so AO's accept-edits
// launches asking and permissionPolicy approves edit requests.
// `--no-auto-update` matches the TUI adapter.
//
// Standing instructions are not passed here: the global `--rules` flag is
// ignored by `grok agent stdio`. They travel as session/new `_meta.rules`; see
// sessionMeta.
func configure(_ context.Context, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
	args := []string{"--no-auto-update"}
	switch launchPermission(cfg.Permissions) {
	case ports.PermissionModeAuto:
		args = append(args, "--permission-mode", "auto", "agent")
	case ports.PermissionModeBypassPermissions:
		args = append(args, "agent", "--always-approve")
	default:
		args = append(args, "--permission-mode", "default", "agent")
	}
	return append(args, "stdio"), nil, nil
}

// sessionMeta delivers AO's standing instructions through Grok's documented
// session/new `_meta.rules`, which Grok appends to its built-in system prompt
// (it lands in the session's `<human_rules>` block). Grok persists the built
// prompt with the session, so a session/load keeps the original rules.
func sessionMeta(cfg acpdriver.LaunchConfig) map[string]any {
	rules := strings.TrimSpace(cfg.SystemPrompt)
	if rules == "" {
		return nil
	}
	return map[string]any{"rules": rules}
}

// launchPermission collapses AO's approval vocabulary onto the three postures
// a Grok ACP process can launch in. Accept-edits shares the asking process.
func launchPermission(mode ports.PermissionMode) ports.PermissionMode {
	switch mode = ports.NormalizePermissionMode(mode); mode {
	case ports.PermissionModeAuto, ports.PermissionModeBypassPermissions:
		return mode
	default:
		return ports.PermissionModeDefault
	}
}

// validateTurnSettings refuses an approval change the live process cannot
// honor. Default and accept-edits share a process, so switching between them
// only changes AO's permissionPolicy; any other change needs a new launch.
func validateTurnSettings(initial ports.PermissionMode, settings ports.ChatTurnSettings) error {
	if settings.Approval == "" {
		return nil
	}
	if launchPermission(initial) == launchPermission(settings.Approval) {
		return nil
	}
	return fmt.Errorf("%w: Grok ACP permission mode change from %q to %q requires restarting Chat",
		ports.ErrChatPermissionModeUnsupported,
		ports.NormalizePermissionMode(initial), ports.NormalizePermissionMode(settings.Approval))
}

// sessionOptions maps AO's per-turn settings onto Grok's live config option
// ids, both advertised from session/new.
func sessionOptions(settings ports.ChatTurnSettings) []acpdriver.SessionOption {
	var options []acpdriver.SessionOption
	if model := strings.TrimSpace(settings.Model); model != "" {
		options = append(options, acpdriver.SessionOption{ID: "model", Value: model})
	}
	if effort := strings.TrimSpace(settings.Effort); effort != "" {
		options = append(options, acpdriver.SessionOption{ID: "reasoning_effort", Value: effort})
	}
	return options
}

// permissionPolicy is AO's side of accept-edits: the process launched asking,
// so AO approves edit-shaped requests once and leaves the rest to the human.
// Every other mode is enforced by Grok itself.
func permissionPolicy(mode ports.PermissionMode, params acpsdk.RequestPermissionRequest) (acpsdk.PermissionOptionId, bool) {
	if ports.NormalizePermissionMode(mode) != ports.PermissionModeAcceptEdits || !isEdit(params.ToolCall.Kind) {
		return "", false
	}
	for _, option := range params.Options {
		if option.Kind == acpsdk.PermissionOptionKindAllowOnce {
			return option.OptionId, true
		}
	}
	return "", false
}

func isEdit(kind *acpsdk.ToolKind) bool {
	return kind != nil &&
		(*kind == acpsdk.ToolKindEdit || *kind == acpsdk.ToolKindDelete || *kind == acpsdk.ToolKindMove)
}
