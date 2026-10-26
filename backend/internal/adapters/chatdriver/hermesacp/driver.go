// Package hermesacp binds the user's own Hermes installation to AO's reusable
// ACP Chat transport.
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
		Configure:         configure,
		SessionMode:       sessionMode,
		SessionOptions:    sessionOptions,
		PermissionPolicy:  permissionPolicy,
		HistoryReconciler: reconcileReplay,
		VersionProbe:      probe,
	}, log)
}

// configure adds provider-native arguments and environment overlays. Hermes'
// ACP server has no launch-time model flag — models are session-owned and
// switched through the ACP config options the session response advertises — and
// no system-prompt flag, so standing instructions ride in
// HERMES_EPHEMERAL_SYSTEM_PROMPT, which Hermes injects at API-call time without
// persisting it into the transcript. Hermes reads that variable first and only
// falls back to its native persona configuration when it is empty, so the
// value folds the persona the session would have applied natively in ahead of
// AO's instructions exactly like the TUI launch path does. The native side is
// resolved by OriginalEphemeralPrompt: an explicit prompt inherited through
// the environment wins over the config.yaml overlay, mirroring GetAgentHooks.
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

// sessionMode maps AO's approval vocabulary onto Hermes' ACP session modes.
// Hermes' modes are edit-approval policies (`default` asks, `accept_edits`
// auto-allows workspace edits, `dont_ask` auto-allows all edits for the
// session), so both of AO's autonomous modes land on `dont_ask`; bypass's
// client-side remainder is handled by permissionPolicy. Default must resolve to
// the explicit `default` mode id: an empty result makes the shared ACP
// conversation skip session/set_mode entirely, so a session started in
// accept_edits or dont_ask keeps auto-allowing edits after the user selects
// Default, because Hermes has already approved them internally.
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

// Hermes' ACP session mode ids, read from the mode list the Hermes ACP server
// advertises in session/new (acp_adapter/server.py _MODES). Pinned here so a
// provider-side rename surfaces as an unknown-mode error instead of silently
// dropping the user's approval choice.
const (
	hermesModeDefault     = "default"
	hermesModeAcceptEdits = "accept_edits"
	hermesModeDontAsk     = "dont_ask"
)

// sessionOptions selects the composer's model like every sibling native ACP
// binding. Hermes advertises provider-prefixed ids (auto:anthropic/claude-…,
// openrouter:anthropic/claude-…) while its configuration names the bare model,
// so the shared legacy model resolve matches the requested value against each
// advertised id's provider-prefixed suffix (acp/config_options.go
// resolveProviderPrefixedChoice); the launch carries no model flag because
// models are session-owned.
//
// Effort is not sent. Hermes' session advertises only `model` and `mode`, and
// like Copilot an effort carried over from another harness's settings must not
// fail the session: rejecting it would also fail every Resume, and AO's Hermes
// composer has no effort control to clear it with.
func sessionOptions(settings ports.ChatTurnSettings) []acpdriver.SessionOption {
	if model := strings.TrimSpace(settings.Model); model != "" {
		return []acpdriver.SessionOption{{ID: "model", Value: model}}
	}
	return nil
}

// permissionPolicy resolves permission requests that have an exact AO policy
// mapping before the generic client parks them for a human. Only bypass
// qualifies: in bypass mode AO answers every Hermes dangerous-command approval
// with allow, because Hermes' ACP server has no server-side bypass switch. The
// answer is always allow_once, even though Hermes also offers allow_session and
// allow_always kinds: Hermes persists those grants in a session cache that
// set_session_mode(default) does not clear (acp_adapter/permissions.py
// _persist_choice, tools/approval.py is_approved), so a command approved during
// bypass would keep running unattended after the user selects Default.
// allow_once keeps every later request governed by whatever AO's policy
// resolves at that time.
func permissionPolicy(
	mode ports.PermissionMode,
	params acpsdk.RequestPermissionRequest,
) (acpsdk.PermissionOptionId, bool) {
	if ports.NormalizePermissionMode(mode) != ports.PermissionModeBypassPermissions {
		return "", false
	}
	// Hermes always offers allow_once first (acp_adapter/permissions.py
	// _build_permission_options). Without it, park the request for a human
	// rather than grant a persistence AO cannot revoke by switching modes.
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
