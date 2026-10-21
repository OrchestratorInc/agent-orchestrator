// Package devinacp binds the user's own Devin for Terminal installation to
// AO's reusable ACP Chat transport.
//
// `devin acp` serves ACP v1 over stdio natively; verified against devin
// 3000.11.92 on 2026-10-21:
//
//	protocolVersion 1
//	loadSession      true            -> ChatCapabilityResume, ChatCapabilityHistory
//	promptCapabilities{image, embeddedContext}
//	sessionCapabilities{list, delete, additionalDirectories, close}
//
// Its session/new advertises five modes and four config options. Every AO
// permission mode has a Devin equivalent, and `model`, `thought_level`, and
// `speed` arrive as live provider-owned config options rather than launch
// flags.
//
// Like every native ACP binding, AO launches the exact binary the existing
// Devin agent plugin resolves. AO never downloads, packages, or substitutes
// the CLI, and registration adds no second answer to "is Devin installed and
// logged in". `devin acp --agent-type summarizer|review` and `--cloud` serve
// other agents and stay unused: Chat runs the default local agent.
package devinacp

import (
	"context"
	"log/slog"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/nativeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Devin's ACP session-mode ids, read off a live 3000.11.92 handshake rather
// than the TUI's --permission-mode vocabulary (auto|accept-edits|smart|
// dangerous): the ACP server advertises accept-edits, smart, ask, plan, and
// bypass. "ask" and "plan" are provider stances AO's approval modes do not
// produce; AO's plan toggle reaches "plan" through the advertised mode config
// option instead.
const (
	modeAcceptEdits = "accept-edits"
	modeSmart       = "smart"
	modeBypass      = "bypass"
)

// New launches `devin acp` from the exact binary resolved by the existing
// Devin agent plugin. Sign-in, models, and session storage remain owned by
// the user's Devin installation.
func New(plugin nativeacp.Plugin, log *slog.Logger) ports.ChatDriver {
	return nativeacp.New(plugin, nativeacp.Config{
		Harness: domain.HarnessDevin,
		Capabilities: ports.ChatCapabilities{
			// Devin reports per-turn token usage in the prompt response, plans
			// through the standard ACP plan update, and advertises a model
			// config option. loadSession is true, so native history loads.
			ports.ChatCapabilityHistory: true,
			ports.ChatCapabilityUsage:   true,
			ports.ChatCapabilityPlans:   true,
			ports.ChatCapabilityModels:  true,
		},
		Configure:       configure,
		SessionMode:     sessionMode,
		SessionOptions:  sessionOptions,
		SessionPreamble: sessionPreamble,
	}, log)
}

// configure builds `devin acp [--model <model>]`. Permission modes are NOT
// passed here: `devin acp` accepts no --permission-mode flag (3000.11.92
// prints "unexpected argument"), so the transport applies them through
// session/set_mode, which the server answers. The model rides the launch
// flag as well as the config option so the initial posture holds even if the
// runtime setter were rejected; see the same reasoning in commandcodeacp.
func configure(_ context.Context, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
	args := []string{"acp"}
	if model := strings.TrimSpace(cfg.Model); model != "" {
		args = append(args, "--model", model)
	}
	return args, nil, nil
}

// sessionMode maps AO's approval vocabulary onto Devin's advertised mode ids.
// Smart auto-approves what the model judges safe, the tier between Code and
// Bypass Permissions that AO's auto selects. An empty return leaves the
// provider's own default — Devin's is accept-edits.
func sessionMode(permission ports.PermissionMode) string {
	switch ports.NormalizePermissionMode(permission) {
	case ports.PermissionModeAcceptEdits:
		return modeAcceptEdits
	case ports.PermissionModeAuto:
		return modeSmart
	case ports.PermissionModeBypassPermissions:
		return modeBypass
	default:
		return ""
	}
}

// sessionOptions maps AO's per-turn model and effort choices onto Devin's
// advertised config option ids, applied through session/set_config_option.
// Values come from Devin's own session catalog, so they pass through
// unchanged.
func sessionOptions(settings ports.ChatTurnSettings) []acpdriver.SessionOption {
	var options []acpdriver.SessionOption
	if model := strings.TrimSpace(settings.Model); model != "" {
		options = append(options, acpdriver.SessionOption{ID: "model", Value: model})
	}
	if effort := strings.TrimSpace(settings.Effort); effort != "" {
		options = append(options, acpdriver.SessionOption{ID: "thought_level", Value: effort})
	}
	return options
}

// sessionPreamble carries AO's standing instructions, with any handoff note,
// on the first prompt: `devin acp` accepts no system-prompt flag and reads no
// session/new metadata, so prompt text is the channel it follows.
func sessionPreamble(cfg acpdriver.LaunchConfig) []acpsdk.ContentBlock {
	if standing := strings.TrimSpace(cfg.SystemPrompt); standing != "" {
		return []acpsdk.ContentBlock{acpdriver.StandingInstructionsBlock(standing)}
	}
	return nil
}
