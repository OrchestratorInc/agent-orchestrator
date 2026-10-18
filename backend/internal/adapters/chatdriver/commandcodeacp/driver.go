// Package commandcodeacp binds the user's own Command Code installation to AO's
// reusable ACP Chat transport.
//
// Command Code ships a first-party ACP agent (`cmd acp`, "Run as an Agent Client
// Protocol (ACP) agent over stdio"), verified against v1.74.0 on 2026-10-02:
//
//	protocolVersion 1
//	loadSession      true            -> ChatCapabilityResume
//	promptCapabilities{image, embeddedContext}
//	mcpCapabilities{http}
//	sessionCapabilities{list, resume, close}
//
// Its session/new advertises five modes and two config options, which is more
// than AO needs to map: every AO permission mode has an exact Command Code
// equivalent, and `model` plus `effort` arrive as live provider-owned config
// options rather than launch flags.
//
// Like every native ACP binding, AO launches the exact binary the existing
// Command Code agent plugin resolves. AO never downloads, packages, or
// substitutes the CLI, and registration here adds no second answer to "is
// Command Code installed and logged in".
package commandcodeacp

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

// Command Code advertises these mode ids from session/new. They were read off a
// live v1.74.0 handshake, not taken from documentation, so an unexpected id
// means the provider changed its vocabulary rather than that AO guessed wrong.
const (
	modeDefault   = "default"
	modeAutoAccpt = "auto-accept"
	modePlan      = "plan"
	modeDontAsk   = "dont-ask"
	modeBypass    = "bypass"
)

// New launches `cmd acp` from the exact binary resolved by the existing
// Command Code agent plugin.
func New(plugin nativeacp.Plugin, log *slog.Logger) ports.ChatDriver {
	return nativeacp.New(plugin, nativeacp.Config{
		Harness: domain.HarnessCommandCode,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityUsage:           true,
			ports.ChatCapabilityDiffs:           true,
			ports.ChatCapabilityPlans:           true,
			ports.ChatCapabilityModels:          true,
			ports.ChatCapabilityConfigOptions:   true,
			ports.ChatCapabilityCompaction:      true,
			ports.ChatCapabilitySkills:          true,
			ports.ChatCapabilityImages:          true,
			ports.ChatCapabilityEmbeddedContext: true,
		},
		Configure:        configure,
		SessionMode:      sessionMode,
		SessionOptions:   sessionOptions,
		SessionPreamble:  sessionPreamble,
		PermissionPolicy: permissionPolicy,
	}, log)
}

// configure builds `cmd acp [flags]`.
//
// The model and permission mode are ALSO passed as launch flags so the initial
// posture survives an agent that rejects the runtime setters with -32601; see
// the same reasoning in kimchiacp. `cmd acp` accepts the same global flags as
// the TUI, so this is one vocabulary, not two.
//
// Standing instructions are NOT passed here: `cmd acp` accepts no system-prompt
// flag (v1.79.x rejects `--append-system-prompt` with "unknown option", which
// kills the provider before the host publishes host.json and surfaces as a
// missing-descriptor startup failure). They ride the first prompt instead; see
// sessionPreamble.
//
// Mods are NOT passed here either: `cmd acp` accepts `--mod`/`--mod-option`
// (and reads settings `mods.paths` / `.commandcode/mods/`) but silently never
// loads them — verified on v1.82.4/v1.83.0, where the same sentinel mod logs
// `factory:ran` under `cmd -p --mod` and logs nothing under `cmd acp --mod`
// after initialize + session/new (issue #6784). Passing --mod would look
// configured while staying inert, so this binding deliberately omits it and
// delivers standing context via sessionPreamble instead.
func configure(_ context.Context, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
	args := []string{
		"acp",
		"--skip-onboarding",
		"--no-auto-update",
		"--trust",
	}
	if model := strings.TrimSpace(cfg.Model); model != "" {
		args = append(args, "--model", model)
	}
	if mode := permissionMode(cfg.Permissions); mode != "" {
		args = append(args, "--permission-mode", mode)
	}
	return args, nil, nil
}

// standingInstructionsURI identifies AO's standing context in the provider's
// own prompt transcript, so a session can be told apart from user context.
const standingInstructionsURI = "ao://agent-orchestrator/standing-instructions"

// sessionPreamble delivers AO's standing instructions as an embedded resource
// on the first prompt of the session.
//
// The workspace SessionStart hook is not a delivery channel for Chat. In
// Command Code v1.79.0 that hook is fired only by the TUI lifecycle
// (`fireSessionStartHooks` has exactly one caller, in the interactive
// component); the ACP path in `bindAgentSession`/`createClientDrivenSession`
// never calls it, so installing `.commandcode/settings.json` changes nothing
// for a Chat session. `session/new` is equally blind: its handler reads only
// `cwd` and `mcpServers`, ignoring `_meta`.
//
// An embedded resource is the one channel this agent does execute. Its
// `promptPart` maps a `resource` block to `<context uri=...>`, which is folded
// into the first turn's input, and session/new advertises
// `promptCapabilities.embeddedContext`. AO therefore leads the first prompt
// with the standing instructions. AO re-arms this on session/load, because
// standing context is process input rather than transcript history.
func sessionPreamble(cfg acpdriver.LaunchConfig) []acpsdk.ContentBlock {
	standing := strings.TrimSpace(cfg.SystemPrompt)
	if standing == "" {
		return nil
	}
	mime := "text/markdown"
	return []acpsdk.ContentBlock{
		acpsdk.ResourceBlock(acpsdk.EmbeddedResourceResource{
			TextResourceContents: &acpsdk.TextResourceContents{
				Uri:      standingInstructionsURI,
				Text:     standing,
				MimeType: &mime,
			},
		}),
	}
}

// sessionMode maps AO's approval vocabulary onto Command Code's advertised mode
// ids. Every AO mode has an exact equivalent, so this never returns "" for a
// mode Command Code actually understands and no approval change needs a turn
// validator: the transport applies it through session/set_mode, and a provider
// that ever answers -32601 surfaces as acpdriver.ErrACPSetterUnsupported.
func sessionMode(mode ports.PermissionMode) string { return permissionMode(mode) }

func permissionMode(mode ports.PermissionMode) string {
	switch ports.NormalizePermissionMode(mode) {
	case ports.PermissionModeAcceptEdits, ports.PermissionModeAuto:
		return modeAutoAccpt
	case ports.PermissionModeBypassPermissions:
		return modeBypass
	default:
		return modeDefault
	}
}

// sessionOptions maps AO's per-turn settings onto Command Code's live config
// option ids. Command Code publishes `model` and `effort` (category
// thought_level) from session/new, so both apply mid-conversation without a
// restart.
func sessionOptions(settings ports.ChatTurnSettings) []acpdriver.SessionOption {
	var options []acpdriver.SessionOption
	if model := strings.TrimSpace(settings.Model); model != "" {
		options = append(options, acpdriver.SessionOption{ID: "model", Value: model})
	}
	if effort := strings.TrimSpace(settings.Effort); effort != "" {
		options = append(options, acpdriver.SessionOption{ID: "effort", Value: effort})
	}
	return options
}

// permissionPolicy leaves every request to the generic human-approval flow.
// Command Code advertises distinct modes rather than resolving approvals per
// request, so auto-approving here would contradict the mode the user selected.
func permissionPolicy(ports.PermissionMode, acpsdk.RequestPermissionRequest) (acpsdk.PermissionOptionId, bool) {
	return "", false
}
