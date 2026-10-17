// Package fxacp binds the user's own fx installation to AO's reusable ACP Chat
// transport.
//
// fx ships a first-party ACP agent (`fx acp`), verified against v0.0.12 on
// 2026-10-15:
//
//	protocolVersion 1
//	loadSession      true            -> ChatCapabilityResume
//	promptCapabilities{image, embeddedContext}
//	mcpCapabilities{http, sse}
//	sessionCapabilities{list, resume, close}
//
// session/new advertises two modes, `ask` (prompt before changes) and `code`
// (fx's auto mode: rules, then a reviewer), and config options `provider`,
// `model`, `mode`, and `effort`. `fx acp` accepts only `--model` and
// `--log-file`; FX_PERMISSION_MODE does not reach ACP sessions, and
// session/set_mode silently ignores `full-access`.
package fxacp

import (
	"context"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/nativeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// fx advertises these mode ids from session/new.
const (
	modeAsk  = "ask"
	modeCode = "code"
)

// New launches `fx acp` from the exact binary resolved by the existing fx
// agent plugin. Login, providers, models, and updates stay with the user's fx
// installation.
func New(plugin nativeacp.Plugin, log *slog.Logger) ports.ChatDriver {
	return nativeacp.New(plugin, nativeacp.Config{
		Harness: domain.HarnessFX,
		Standby: &acpdriver.StandbyConfig{},
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityModels:        true,
			ports.ChatCapabilityConfigOptions: true,
		},
		Configure:        configure,
		SessionMode:      sessionMode,
		SessionOptions:   sessionOptions,
		SessionPreamble:  sessionPreamble,
		PermissionPolicy: permissionPolicy,
		ProviderNotice:   providerNotice,
	}, log)
}

// configure builds `fx acp`. Model, effort, and mode are all live session
// settings, so the launch carries nothing per session. fx has no
// system-prompt flag or session/new `_meta` for standing instructions; they
// ride the first prompt instead (see sessionPreamble).
func configure(_ context.Context, _ acpdriver.LaunchConfig) ([]string, map[string]string, error) {
	return []string{"acp"}, nil, nil
}

// standingInstructionsFile is where the fx driver keeps a session's standing
// instructions, in the session's prompt directory under AO's data dir.
const standingInstructionsFile = "fx-standing-instructions.md"

// sessionPreamble delivers AO's standing instructions on the first prompt; fx
// advertises promptCapabilities.embeddedContext. The transport re-arms it on
// session/load, because standing context is process input rather than
// transcript history.
//
// fx takes an embedded resource as project instructions only when its URI
// names a readable local file ("unsafe target" otherwise, and the
// instructions are dropped). It reads the embedded text, not the file, so the
// file only has to exist. If it cannot be written, the instructions go as a
// plain text block, which fx also reads.
func sessionPreamble(cfg acpdriver.LaunchConfig) []acpsdk.ContentBlock {
	standing := strings.TrimSpace(cfg.SystemPrompt)
	if standing == "" {
		return nil
	}
	path, err := writeStandingInstructions(cfg, standing)
	if err != nil {
		return []acpsdk.ContentBlock{acpsdk.TextBlock(standing)}
	}
	mime := "text/markdown"
	return []acpsdk.ContentBlock{
		acpsdk.ResourceBlock(acpsdk.EmbeddedResourceResource{
			TextResourceContents: &acpsdk.TextResourceContents{
				Uri:      (&url.URL{Scheme: "file", Path: path}).String(),
				Text:     standing,
				MimeType: &mime,
			},
		}),
	}
}

func writeStandingInstructions(cfg acpdriver.LaunchConfig, standing string) (string, error) {
	if strings.TrimSpace(cfg.DataDir) == "" || strings.TrimSpace(string(cfg.SessionID)) == "" {
		return "", os.ErrInvalid
	}
	dir := filepath.Join(cfg.DataDir, "prompts", filepath.Base(string(cfg.SessionID)))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, standingInstructionsFile)
	if err := os.WriteFile(path, []byte(standing+"\n"), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// providerNotice matches the diagnostics fx writes into the answer stream
// ahead of its reply: context assembly notes ("[context] ...") and skill
// discovery warnings about the user's skill directories.
func providerNotice(text string) bool {
	text = strings.TrimSpace(text)
	return strings.HasPrefix(text, "[context] ") || strings.HasPrefix(text, "skill discovery warning:")
}

// sessionMode maps AO's approval vocabulary onto fx's ACP modes. Only auto
// uses fx's reviewer (`code`). Accept-edits and bypass stay in `ask` so every
// sensitive call reaches AO, where permissionPolicy approves the calls the AO
// mode allows; fx's own `code` reviewer could otherwise deny a call bypass
// should run.
func sessionMode(mode ports.PermissionMode) string {
	if ports.NormalizePermissionMode(mode) == ports.PermissionModeAuto {
		return modeCode
	}
	return modeAsk
}

// sessionOptions maps AO's per-turn settings onto fx's live config option ids.
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

// permissionPolicy answers the requests fx's `ask` mode sends: bypass approves
// every one, accept-edits approves edit-shaped ones, and everything else goes
// to the human.
func permissionPolicy(mode ports.PermissionMode, params acpsdk.RequestPermissionRequest) (acpsdk.PermissionOptionId, bool) {
	switch ports.NormalizePermissionMode(mode) {
	case ports.PermissionModeBypassPermissions:
	case ports.PermissionModeAcceptEdits:
		if !isEdit(params.ToolCall.Kind) {
			return "", false
		}
	default:
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
