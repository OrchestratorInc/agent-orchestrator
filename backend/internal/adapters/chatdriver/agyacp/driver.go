// Package agyacp binds Google Antigravity to AO's reusable ACP Chat transport.
//
// The agy CLI has no ACP mode. Google publishes a separate ACP server,
// `agy_acp_server`, through the ACP registry (`antigravity-acp`). Like
// Autohand's separately distributed `autohand-acp`, AO launches the user's own
// installed copy, found beside the agy CLI or on PATH, and never downloads or
// bundles it. Login, models, MCP configuration, and conversations stay the
// user's own under GEMINI_HOME (default ~/.gemini).
package agyacp

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"runtime"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agy"
	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Plugin is the existing Agy agent-plugin surface the binding reuses: the agy
// CLI's location, beside which the ACP server is usually installed, and the
// canonical auth probe. The server itself is resolved separately because it is
// not the agy CLI.
type Plugin interface {
	ResolveBinary(context.Context) (string, error)
	AuthStatus(context.Context) (ports.AgentAuthStatus, error)
}

// Antigravity's ACP mode ids, as advertised by agy_acp_server 1.3.0.
const (
	modeDefault  = "default"
	modeAutoEdit = "auto_edit"
	modeYolo     = "yolo"
)

// thinkingLevelOption is the config option agy_acp_server 1.4 moved reasoning
// levels into. Its model option then lists gemini-3.8-flash where 1.3 and the
// agy CLI list gemini-3.8-flash-high.
const thinkingLevelOption = "thinking_level"

var modelVariantPattern = regexp.MustCompile(`^(.+)-(minimal|low|medium|high|xhigh)$`)

// New constructs the Antigravity ACP Chat driver.
func New(plugin Plugin, log *slog.Logger) ports.ChatDriver {
	var agyCLI func(context.Context) (string, error)
	if plugin != nil {
		agyCLI = plugin.ResolveBinary
	}
	return acpdriver.New(buildConfig(plugin, newResolver(agyCLI), log), log)
}

func buildConfig(plugin Plugin, resolve resolver, log *slog.Logger) acpdriver.Config {
	return acpdriver.Config{
		Harness: domain.HarnessAgy,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityStreaming: true,
			ports.ChatCapabilityTools:     true,
			// Antigravity raises session/request_permission for every gated tool.
			ports.ChatCapabilityApprovals: true,
			ports.ChatCapabilityInterrupt: true,
			ports.ChatCapabilityResume:    true,
		},
		InstallCheck: func(ctx context.Context) error {
			_, err := resolve.serverPath(ctx)
			return err
		},
		Probe: func(ctx context.Context) error {
			if _, err := resolve.serverPath(ctx); err != nil {
				return fmt.Errorf("%w: %w", ports.ErrChatDriverUnavailable, err)
			}
			if plugin != nil {
				status, err := plugin.AuthStatus(ctx)
				if err == nil && status == ports.AgentAuthStatusUnauthorized {
					return ports.ErrChatAuthRequired
				}
				if err != nil && log != nil {
					// The ACP handshake stays authoritative for an inconclusive probe.
					log.Debug("agy auth probe inconclusive; continuing", "error", err)
				}
			}
			return nil
		},
		Launch: func(ctx context.Context, cfg acpdriver.LaunchConfig) (acpdriver.Launch, error) {
			server, err := resolve.serverPath(ctx)
			if err != nil {
				return acpdriver.Launch{}, fmt.Errorf("%w: %w", ports.ErrChatDriverUnavailable, err)
			}
			env := make(map[string]string, len(cfg.Env)+2)
			for key, value := range cfg.Env {
				env[key] = value
			}
			if env[harnessPathEnvVar] == "" {
				if harness := resolve.harnessPath(server); harness != "" {
					env[harnessPathEnvVar] = harness
				}
			}
			if env[disableWorkspaceTrustEnvVar] == "" {
				env[disableWorkspaceTrustEnvVar] = "1"
			}
			return acpdriver.Launch{Command: server, Args: launchArgs(runtime.GOOS), Env: env}, nil
		},
		ValidateInitialize: validateInitialize,
		AfterInitialize:    newAuthBootstrap(agy.GeminiHome, cliSignedIn(resolve.agyCLI)).run,
		SessionMode:        sessionMode,
		SessionOptions:     sessionOptions,
		SplitModelVariant:  splitModelVariant,
		SessionPreamble:    sessionPreamble,
		// No PermissionPolicy: every request Antigravity raises is answered by
		// the user. AO never selects allow_always for them, which would let a
		// cached grant survive a later switch to a stricter approval mode.
	}
}

// launchArgs mirrors the registry entry's per-platform arguments. The Linux
// build requires an explicit empty --uid flag.
func launchArgs(goos string) []string {
	if goos == "linux" {
		return []string{"--uid="}
	}
	return nil
}

// sessionMode maps AO's approval vocabulary onto Antigravity's mode ids.
// Antigravity has no classifier mode between auto_edit (edits only) and yolo
// (all tools). AO's auto means "run without asking", which is how the
// orchestrator and long worker turns operate, so it selects yolo, like Goose's
// auto. Default keeps every gated tool behind an approval.
func sessionMode(permission ports.PermissionMode) string {
	switch ports.NormalizePermissionMode(permission) {
	case ports.PermissionModeDefault:
		return modeDefault
	case ports.PermissionModeAcceptEdits:
		return modeAutoEdit
	case ports.PermissionModeAuto, ports.PermissionModeBypassPermissions:
		return modeYolo
	default:
		return ""
	}
}

// sessionOptions selects the chosen model. It is optional because a task's model
// comes from the agy CLI's list, which is wider than what the ACP server offers:
// a session on a model it lacks keeps its default, and the chat's own picker
// lists what the server really has.
func sessionOptions(settings ports.ChatTurnSettings) []acpdriver.SessionOption {
	if model := strings.TrimSpace(settings.Model); model != "" {
		return []acpdriver.SessionOption{{ID: "model", Value: model, Optional: true}}
	}
	return nil
}

// splitModelVariant turns gemini-3.8-flash-high into what server 1.4 offers: the
// gemini-3.8-flash model at thinking level high.
func splitModelVariant(model string) (string, acpdriver.SessionOption, bool) {
	match := modelVariantPattern.FindStringSubmatch(model)
	if match == nil {
		return "", acpdriver.SessionOption{}, false
	}
	return match[1], acpdriver.SessionOption{ID: thinkingLevelOption, Value: match[2], Optional: true}, true
}

// sessionPreamble delivers AO's standing instructions as a text block ahead of
// the first prompt. Antigravity accepts no system-prompt launch flag, and it
// treats an embedded resource as something to look up (it web-searched the
// resource URI in testing) rather than as instructions, so plain text is the
// channel it follows. The instructions do not depend on any mode the user can
// switch, so they survive approval changes.
func sessionPreamble(cfg acpdriver.LaunchConfig) []acpsdk.ContentBlock {
	standing := strings.TrimSpace(cfg.SystemPrompt)
	if standing == "" {
		return nil
	}
	return []acpsdk.ContentBlock{acpsdk.TextBlock(
		"Standing instructions from Agent Orchestrator. Follow them for this whole session:\n" +
			"<ao_standing_instructions>\n" + standing + "\n</ao_standing_instructions>",
	)}
}
