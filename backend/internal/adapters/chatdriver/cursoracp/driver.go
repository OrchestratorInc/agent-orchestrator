// Package cursoracp binds the user's own Cursor Agent installation to AO's
// reusable ACP Chat transport.
package cursoracp

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/nativeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// New launches `cursor-agent acp` from the exact binary resolved by the
// existing Cursor adapter. Models and slash commands come from the live ACP
// session advertisements. Cursor owns tools, project rules, and auth; AO adds a
// session-scoped standing rule and resolves permissions through ACP.
func New(plugin nativeacp.Plugin, log *slog.Logger) ports.ChatDriver {
	return nativeacp.New(plugin, nativeacp.Config{
		Harness:          domain.HarnessCursor,
		Configure:        configure,
		SessionOptions:   sessionOptions,
		SessionPreamble:  sessionPreamble,
		PermissionPolicy: permissionPolicy,
		ClientExtension:  handleExtension,
		ClientExtensionAliases: map[string]string{
			askQuestionMethod: "_cursor/ask_question",
			createPlanMethod:  "_cursor/create_plan",
		},
		ValidateTurnSettings: validateTurnSettings,
		VersionProbe:         versionProbe,
		// Cursor has no session/close and ignores stdin closing; it exits on
		// SIGTERM in about 50ms.
		QuickStop: true,
	}, log)
}

func validateTurnSettings(initial ports.PermissionMode, settings ports.ChatTurnSettings) error {
	if err := validateCursorFastParam(settings.Model); err != nil {
		return err
	}
	if settings.Approval == "" {
		return nil
	}
	initial = ports.NormalizePermissionMode(initial)
	requested := ports.NormalizePermissionMode(settings.Approval)
	if initial == requested {
		return nil
	}
	if cursorProcessPermissionMode(initial) || cursorProcessPermissionMode(requested) {
		return fmt.Errorf(
			"cursor ACP permission mode change from %q to %q requires restarting Chat",
			initial, requested,
		)
	}
	return nil
}

func validateCursorFastParam(model string) error {
	_, params, _ := cursorModelParams(model)
	value, ok := params["fast"]
	if ok && value != "true" && value != "false" {
		return fmt.Errorf("cursor model parameter \"fast\" must be true or false, got %q", value)
	}
	return nil
}

func cursorProcessPermissionMode(mode ports.PermissionMode) bool {
	return mode == ports.PermissionModeAuto || mode == ports.PermissionModeBypassPermissions
}

func configure(ctx context.Context, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
	args := make([]string, 0, 4)
	switch ports.NormalizePermissionMode(cfg.Permissions) {
	case ports.PermissionModeAuto:
		args = append(args, "--auto-review")
	case ports.PermissionModeBypassPermissions:
		args = append(args, "--force")
	}
	args = append(args, "acp")
	var env map[string]string
	if strings.TrimSpace(cfg.DataDir) != "" {
		// Earlier builds left each session's standing rules, its full system
		// prompt, in plugin folders that nothing reads any more.
		_ = os.RemoveAll(filepath.Join(cfg.DataDir, "cursor", "acp-plugins"))
		// The preload only speeds Cursor up; any problem launches it without one.
		if preload, err := writeNetPreload(cfg.DataDir); err == nil && preloadPathUsable(preload) {
			existing, ok := cfg.Env["NODE_OPTIONS"]
			if !ok {
				existing = os.Getenv("NODE_OPTIONS")
			}
			env = map[string]string{"NODE_OPTIONS": nodeOptionsWithPreload(existing, preload)}
		}
	}
	return args, env, nil
}

// sessionPreamble leads the first prompt with AO's standing instructions (and a
// switch's handoff note): Cursor's chat reads neither a --plugin-dir rule nor an
// embedded resource (Cursor Agent 2026.10.01), only prompt text.
func sessionPreamble(cfg acpdriver.LaunchConfig) []acpsdk.ContentBlock {
	if standing := strings.TrimSpace(cfg.SystemPrompt); standing != "" {
		return []acpsdk.ContentBlock{acpdriver.StandingInstructionsBlock(standing)}
	}
	return nil
}

func permissionPolicy(
	mode ports.PermissionMode,
	params acpsdk.RequestPermissionRequest,
) (acpsdk.PermissionOptionId, bool) {
	mode = ports.NormalizePermissionMode(mode)
	if mode == ports.PermissionModeAcceptEdits {
		kind := acpsdk.ToolKind("")
		if params.ToolCall.Kind != nil {
			kind = *params.ToolCall.Kind
		}
		if kind != acpsdk.ToolKindEdit && kind != acpsdk.ToolKindDelete && kind != acpsdk.ToolKindMove {
			return "", false
		}
		return permissionOption(params.Options, acpsdk.PermissionOptionKindAllowOnce)
	}
	if mode == ports.PermissionModeBypassPermissions {
		if id, ok := permissionOption(params.Options, acpsdk.PermissionOptionKindAllowAlways); ok {
			return id, true
		}
		return permissionOption(params.Options, acpsdk.PermissionOptionKindAllowOnce)
	}
	return "", false
}

func permissionOption(
	options []acpsdk.PermissionOption,
	kind acpsdk.PermissionOptionKind,
) (acpsdk.PermissionOptionId, bool) {
	for _, option := range options {
		if option.Kind == kind {
			return option.OptionId, true
		}
	}
	return "", false
}

// sessionOptions splits a model id that folds in effort, thinking and speed (the
// command line's "claude-opus-4-8-thinking-high-fast", an older session's
// "claude-opus-4-8[thinking=true,effort=high]") into Cursor's separate settings.
// A plain id sets only the model, keeping the settings Cursor saved for it.
func sessionOptions(settings ports.ChatTurnSettings) []acpdriver.SessionOption {
	if settings.Model == "" {
		return nil
	}
	model, params, explicitFast := cursorModelParams(settings.Model)
	if settings.Effort != "" {
		params["effort"] = settings.Effort
	}
	options := []acpdriver.SessionOption{{ID: "model", Value: model}}
	for _, id := range []string{"thinking", "fast"} {
		if value, ok := params[id]; ok && (id != "thinking" || value != "") {
			// Only an explicitly selected Fast setting must fail closed. CLI-style
			// aliases synthesize fast=false when omitted; Thinking remains best-effort.
			options = append(options, acpdriver.SessionOption{
				ID: id, Value: value, Optional: id == "thinking" || (id == "fast" && !explicitFast),
			})
		}
	}
	// Each model names its effort setting its own way; only the one it offers applies.
	if effort := params["effort"]; effort != "" {
		options = append(options,
			acpdriver.SessionOption{ID: "effort", Value: effort, Optional: true},
			acpdriver.SessionOption{ID: "reasoning_effort", Value: effort, Optional: true},
			acpdriver.SessionOption{ID: "reasoning", Value: strings.ReplaceAll(effort, "xhigh", "extra-high"), Optional: true})
	}
	return options
}

func cursorModelParams(id string) (string, map[string]string, bool) {
	params := map[string]string{}
	explicitFast := false
	if base, body, bracketed := strings.Cut(strings.TrimSuffix(id, "]"), "["); bracketed {
		for _, param := range strings.Split(body, ",") {
			key, value, _ := strings.Cut(strings.TrimSpace(param), "=")
			if key == "reasoning" || key == "reasoning_effort" {
				key = "effort"
			}
			params[key] = value
			if key == "fast" {
				explicitFast = true
			}
		}
		return base, params, explicitFast
	}
	tokens := strings.Split(strings.ReplaceAll(id, "-extra-high", "-xhigh"), "-")
	end := len(tokens)
	for ; end > 1; end-- {
		last := tokens[end-1]
		if last == "fast" || last == "thinking" {
			params[last] = "true"
			explicitFast = explicitFast || last == "fast"
		} else if slices.Contains(cursorEfforts, last) && params["effort"] == "" {
			params["effort"] = last
		} else {
			break
		}
	}
	if len(params) == 0 {
		if id == "auto" {
			id = "default"
		}
		return id, params, explicitFast
	}
	// A command-line id that leaves a setting out means it is off.
	for _, flag := range []string{"thinking", "fast"} {
		if params[flag] == "" {
			params[flag] = "false"
		}
	}
	return cursorChatModelName(strings.Join(tokens[:end], "-")), params, explicitFast
}

// cursorOlderClaude matches the command line's older Claude name, version first
// ("claude-4.6-opus"), which Cursor's chat names family first ("claude-opus-4-6").
var cursorOlderClaude = regexp.MustCompile(`^claude-(\d+(?:\.\d+)?)-(opus|sonnet|haiku)$`)

// cursorChatModelName turns a command-line model name into the one Cursor's chat lists.
func cursorChatModelName(name string) string {
	if match := cursorOlderClaude.FindStringSubmatch(name); match != nil {
		return "claude-" + match[2] + "-" + strings.ReplaceAll(match[1], ".", "-")
	}
	return strings.TrimPrefix(name, "cursor-")
}

var cursorEfforts = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}
