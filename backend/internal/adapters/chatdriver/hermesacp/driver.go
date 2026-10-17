// Package hermesacp binds the user's own Hermes installation to AO's reusable
// ACP Chat transport.
package hermesacp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/nativeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// hermesACPVersionTimeout bounds the `hermes acp --version` admission probe.
const hermesACPVersionTimeout = 10 * time.Second

// New launches `hermes acp` from the exact binary resolved by the existing
// Hermes agent plugin. Hermes keeps filesystem and terminal execution in its
// own process; AO advertises neither ACP client capability and receives typed
// session updates and permission requests.
func New(plugin nativeacp.Plugin, log *slog.Logger) ports.ChatDriver {
	return newDriver(plugin, versionProbe, log)
}

func newDriver(plugin nativeacp.Plugin, probe nativeacp.VersionProbe, log *slog.Logger) ports.ChatDriver {
	return nativeacp.New(plugin, nativeacp.Config{
		Harness: domain.HarnessHermes,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityDiffs: true,
		},
		Configure:        configure,
		SessionMode:      sessionMode,
		PermissionPolicy: permissionPolicy,
		VersionProbe:     probe,
	}, log)
}

// configure adds provider-native arguments and environment overlays. Hermes'
// ACP server has no launch-time model flag — models are session-owned and
// switched through the ACP config options the session response advertises — and
// no system-prompt flag, so standing instructions ride in
// HERMES_EPHEMERAL_SYSTEM_PROMPT, which Hermes injects at API-call time without
// persisting it into the transcript.
func configure(_ context.Context, cfg acpdriver.LaunchConfig) ([]string, map[string]string, error) {
	args := []string{"acp"}
	env := map[string]string(nil)
	if prompt := strings.TrimSpace(cfg.SystemPrompt); prompt != "" {
		env = map[string]string{"HERMES_EPHEMERAL_SYSTEM_PROMPT": prompt}
	}
	return args, env, nil
}

// sessionMode maps AO's approval vocabulary onto Hermes' ACP session modes.
// Hermes' modes are edit-approval policies (`default` asks, `accept_edits`
// auto-allows workspace edits, `dont_ask` auto-allows all edits for the
// session), so both of AO's autonomous modes land on `dont_ask`; bypass's
// client-side remainder is handled by permissionPolicy.
func sessionMode(mode ports.PermissionMode) string {
	switch ports.NormalizePermissionMode(mode) {
	case ports.PermissionModeAcceptEdits:
		return "accept_edits"
	case ports.PermissionModeAuto, ports.PermissionModeBypassPermissions:
		return "dont_ask"
	default:
		return ""
	}
}

// permissionPolicy resolves permission requests that have an exact AO policy
// mapping before the generic client parks them for a human. Only bypass
// qualifies: in bypass mode AO answers every Hermes dangerous-command approval
// with allow, because Hermes' ACP server has no server-side bypass switch.
func permissionPolicy(
	mode ports.PermissionMode,
	params acpsdk.RequestPermissionRequest,
) (acpsdk.PermissionOptionId, bool) {
	if ports.NormalizePermissionMode(mode) != ports.PermissionModeBypassPermissions {
		return "", false
	}
	if id, ok := permissionOption(params.Options, acpsdk.PermissionOptionKindAllowAlways); ok {
		return id, true
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

// versionProbe gates admission on `hermes acp --version` succeeding, which
// proves the resolved executable's ACP entry point imports and answers before
// a session launch pays for the full startup.
func versionProbe(ctx context.Context, bin string) error {
	probeCtx, cancel := context.WithTimeout(ctx, hermesACPVersionTimeout)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, bin, "acp", "--version") //nolint:gosec // binary is adapter-resolved; args are static
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
