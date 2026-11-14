package hermesacp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hermes"
	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/nativeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const hermesACPVersionTimeout = 10 * time.Second

func New(plugin nativeacp.Plugin, log *slog.Logger) ports.ChatDriver {
	return newDriver(plugin, versionProbe, log)
}

func newDriver(plugin nativeacp.Plugin, probe nativeacp.VersionProbe, log *slog.Logger) ports.ChatDriver {
	return nativeacp.New(plugin, nativeacp.Config{
		Harness: domain.HarnessHermes,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityDiffs: true,
		},
		Configure:                    configure,
		SessionMode:                  sessionMode,
		SessionOptions:               sessionOptions,
		SessionPreamble:              SessionPreamble,
		ResetsConversation:           resetsConversation,
		PermissionPolicy:             permissionPolicy,
		HistoryReconciler:            reconcileReplay,
		PromptErrorFailure:           promptErrorFailure,
		VersionProbe:                 probe,
		SteerPrompt:                  steerPrompt,
		ImplicitWorkspaceDirectories: true,
		SuppressSessionModes:         true,
	}, log)
}

const outputLanguageInstruction = "Output language: all user-visible output (narration, tool-call text, permission and edit approval requests, diff context, commit and PR text) must be in English. Never output Chinese."

func SessionPreamble(cfg acpdriver.LaunchConfig) []acpsdk.ContentBlock {
	standing := strings.TrimSpace(cfg.SystemPrompt)
	if standing == "" {
		return nil
	}
	return []acpsdk.ContentBlock{acpdriver.StandingInstructionsBlock(
		standing + "\n" + outputLanguageInstruction,
	)}
}

func resetsConversation(prompt []acpsdk.ContentBlock) bool {
	var text strings.Builder
	for _, block := range prompt {
		if block.Text == nil {
			return false
		}
		text.WriteString(block.Text.Text)
	}
	fields := strings.Fields(text.String())
	if len(fields) == 0 {
		return false
	}
	switch strings.ToLower(fields[0]) {
	case "/reset", "/new", "/clear":
		return true
	}
	return false
}

func configure(_ context.Context, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
	args := []string{"acp"}
	env := map[string]string(nil)
	if prompt := strings.TrimSpace(cfg.SystemPrompt); prompt != "" {
		persona, err := hermes.OriginalEphemeralPrompt(cfg.Env)
		if err != nil {
			return nil, nil, err
		}
		combined, err := hermes.ComposeEphemeralPrompt(persona, prompt)
		if err != nil {
			return nil, nil, err
		}
		env = map[string]string{"HERMES_EPHEMERAL_SYSTEM_PROMPT": combined}
	}
	return args, env, nil
}

func sessionMode(mode ports.PermissionMode) string {
	switch ports.NormalizePermissionMode(mode) {
	case ports.PermissionModeDefault:
		return hermesModeDefault
	case ports.PermissionModeAcceptEdits:
		return hermesModeAcceptEdits
	case ports.PermissionModeAuto, ports.PermissionModeBypassPermissions:
		return hermesModeDontAsk
	default:
		return ""
	}
}

const (
	hermesModeDefault     = "default"
	hermesModeAcceptEdits = "accept_edits"
	hermesModeDontAsk     = "dont_ask"
)

func sessionOptions(settings ports.ChatTurnSettings) []acpdriver.SessionOption {
	if model := strings.TrimSpace(settings.Model); model != "" {
		return []acpdriver.SessionOption{{ID: "model", Value: model}}
	}
	return nil
}

func permissionPolicy(
	mode ports.PermissionMode,
	params acpsdk.RequestPermissionRequest,
) (acpsdk.PermissionOptionId, bool) {
	if ports.NormalizePermissionMode(mode) != ports.PermissionModeBypassPermissions {
		return "", false
	}
	return permissionOption(params.Options, acpsdk.PermissionOptionKindAllowOnce)
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

func versionProbe(ctx context.Context, bin string) error {
	probeCtx, cancel := context.WithTimeout(ctx, hermesACPVersionTimeout)
	defer cancel()
	cmd := aoprocess.CommandContext(probeCtx, bin, "acp", "--version")
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("hermes acp --version: %w", err)
	}
	if strings.TrimSpace(string(out)) == "" {
		return errors.New("hermes acp --version printed no version")
	}
	return nil
}
