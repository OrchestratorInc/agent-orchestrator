// Package museacp binds the user's own Muse Code installation to AO's reusable
// ACP Chat transport through `muse acp`.
package museacp

import (
	"context"
	"log/slog"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/muse"
	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/nativeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// experimentalACPEnvVar opens `muse acp`'s gate. It is open by default from
// 1.4, but remote configuration can close it; this value reopens it.
const experimentalACPEnvVar = "MUSE_EXPERIMENTAL_ACP"

// Muse Code's ACP mode ids: its approval modes, chosen per session.
const (
	modeAllowAll  = "allowAll"
	modeOnRequest = "onRequest"
)

// New launches `muse acp` from the binary resolved by the Muse Code agent
// plugin. Login, models, settings, sessions, and updates stay with the user's
// install. A prompt sent while a turn runs waits for that turn, so a message
// AO sends to a busy muse chat is queued as its next input.
func New(plugin nativeacp.Plugin, log *slog.Logger) ports.ChatDriver {
	return newDriver(plugin, versionProbe, log)
}

func newDriver(plugin nativeacp.Plugin, probe nativeacp.VersionProbe, log *slog.Logger) ports.ChatDriver {
	return nativeacp.New(plugin, nativeacp.Config{
		Harness:      domain.HarnessMuse,
		Configure:    configure,
		SessionMode:  sessionMode,
		HideCommand:  hiddenCommand,
		VersionProbe: probe,
		Capabilities: ports.ChatCapabilities{
			// Muse asks over session/request_permission in its asking modes.
			ports.ChatCapabilityApprovals: true,
		},
	}, log)
}

// configure starts `muse acp` the way AO starts Muse's terminal UI: with the
// workspace's own skills and rules, the session's model, and AO's standing
// instructions in the developer prompt. Approval is a session mode, not a flag
// (`muse acp` refuses --yolo and --approval-mode); bypass also turns off the
// sandbox, as --yolo does in the terminal.
func configure(_ context.Context, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
	args := []string{"acp", "--trust-workspace"}
	if ports.NormalizePermissionMode(cfg.Permissions) == ports.PermissionModeBypassPermissions {
		args = append(args, "--disable-sandbox")
	}
	if model := strings.TrimSpace(cfg.Model); model != "" {
		args = append(args, "--model", model)
	}
	env := map[string]string{experimentalACPEnvVar: "on"}
	if prompt := strings.TrimSpace(cfg.SystemPrompt); prompt != "" {
		env[muse.DeveloperPromptEnvVar] = prompt
	}
	return args, env, nil
}

// sessionMode maps AO's approvals onto Muse's modes as the terminal UI maps
// them onto its flags: AO's default keeps Muse's own (ask on request), and
// every mode that runs without asking allows all.
func sessionMode(permission ports.PermissionMode) string {
	switch ports.NormalizePermissionMode(permission) {
	case ports.PermissionModeDefault:
		return modeOnRequest
	case ports.PermissionModeAcceptEdits, ports.PermissionModeAuto, ports.PermissionModeBypassPermissions:
		return modeAllowAll
	default:
		return ""
	}
}
