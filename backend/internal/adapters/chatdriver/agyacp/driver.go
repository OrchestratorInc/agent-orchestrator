// Package agyacp binds Google Antigravity to AO's reusable ACP Chat transport.
//
// The agy CLI has a built-in ACP server, `agy acp`, which steers a running turn
// when another prompt arrives. AO launches it from agy 1.5.4, the first release
// whose `agy acp` does not hang on a second approval. Older CLIs have no ACP
// mode, or that bug, so AO falls back to
// Google's separate `agy_acp_server` from the ACP registry (`antigravity-acp`),
// found beside the agy CLI or on PATH. Like Autohand's separately distributed
// `autohand-acp`, AO never downloads or bundles either. Login, models, MCP
// configuration, and conversations stay the user's own under GEMINI_HOME
// (default ~/.gemini); both servers keep Chat conversations in the same
// antigravity-acp folder.
package agyacp

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"runtime"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/adapters/agent/agy"
	acpdriver "github.com/OrchestratorInc/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
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
// agy CLI list gemini-3.8-flash-high. `agy acp` 1.5 splits its models the same
// way but names the option thought_level; the ACP driver then sends the level to
// the session's thought_level-category option instead.
const thinkingLevelOption = "thinking_level"

var modelVariantPattern = regexp.MustCompile(`^(.+)-(minimal|low|medium|high|xhigh)$`)

// `agy acp`'s mode ids. It has no mode that runs every tool without asking;
// that is a launch flag, and AO's approval can change between turns. 1.5.4's
// `auto` still asks before a command leaves the terminal sandbox, so AO's
// auto, which runs without asking, does not map onto it.
const cliModeAcceptEdits = "accept-edits"

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
		Harness:     domain.HarnessAgy,
		HideCommand: hiddenCommands,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityStreaming: true,
			ports.ChatCapabilityTools:     true,
			// Antigravity raises session/request_permission for every gated tool.
			ports.ChatCapabilityApprovals: true,
			ports.ChatCapabilityInterrupt: true,
			ports.ChatCapabilityResume:    true,
		},
		InstallCheck: func(ctx context.Context) error {
			_, err := resolve.target(ctx)
			return err
		},
		Probe: func(ctx context.Context) error {
			if _, err := resolve.target(ctx); err != nil {
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
			target, err := resolve.target(ctx)
			if err != nil {
				return acpdriver.Launch{}, fmt.Errorf("%w: %w", ports.ErrChatDriverUnavailable, err)
			}
			env := make(map[string]string, len(cfg.Env)+2)
			for key, value := range cfg.Env {
				env[key] = value
			}
			if env[disableWorkspaceTrustEnvVar] == "" {
				env[disableWorkspaceTrustEnvVar] = "1"
			}
			root, err := agy.GeminiHome(env)
			if err != nil {
				return acpdriver.Launch{}, err
			}
			binary := target.path
			if !target.cli {
				binary = ""
			}
			if err := agy.ValidateImportedACP(ctx, root, cfg.ProviderConversationID, cfg.WorkspacePath, binary); err != nil {
				return acpdriver.Launch{}, err
			}
			if target.cli {
				return acpdriver.Launch{Command: target.path, Args: cliLaunchArgs(env, resolve.getenv), Env: env}, nil
			}
			if env[harnessPathEnvVar] == "" {
				if harness := resolve.harnessPath(target.path); harness != "" {
					env[harnessPathEnvVar] = harness
				}
			}
			return acpdriver.Launch{Command: target.path, Args: launchArgs(runtime.GOOS), Env: env}, nil
		},
		ValidateInitialize:  validateInitialize,
		AfterInitialize:     afterInitialize(newAuthBootstrap(agy.GeminiHome, cliSignedIn(resolve.agyCLI))),
		SessionMode:         sessionMode,
		SessionModeForAgent: sessionModeForAgent,
		OptionalDirectories: func(cfg acpdriver.LaunchConfig) []string {
			return agy.SkillDirectories(cfg.DataDir, string(ports.SkillsOf(cfg.SessionID, cfg.SkillSessionID)))
		},
		SessionOptions:    sessionOptions,
		SplitModelVariant: splitModelVariant,
		SessionPreamble:   sessionPreamble,
		// Every request Antigravity raises is answered by the user, except under
		// `agy acp` in AO's auto modes (see permissionPolicy). AO never selects
		// allow_always, which would let a cached grant survive a later switch to
		// a stricter approval mode.
		PermissionPolicy: permissionPolicy,
		SteerPrompt:      &acpdriver.SteerPrompt{Supported: cliSteers},
		// run_command hands a command to the background once its
		// WaitMsBeforeAsync passes, and the turn can end while it runs.
		// Antigravity reports its exit later, then answers it in a cycle of its
		// own, so the command is a background task, not a failed tool.
		ToolsOutliveTurn: true,
	}
}

// cliLaunchArgs starts `agy acp` with its Chat data in antigravity-acp, where
// the separate server keeps it, so AO's history checks and the Chat/terminal
// handoff find it, and a Chat started on the separate server resumes. `agy acp`
// ignores the GEMINI_HOME variable and stores under its --gemini-dir, so a
// GEMINI_HOME set for the session or inherited by the daemon is passed as that
// flag, the same precedence agy.GeminiHome uses.
func cliLaunchArgs(env map[string]string, getenv func(string) string) []string {
	args := []string{"acp", "--app-data-dir", agy.ACPStorageDir}
	home := strings.TrimSpace(env["GEMINI_HOME"])
	if home == "" {
		home = strings.TrimSpace(getenv("GEMINI_HOME"))
	}
	if home != "" {
		args = append(args, "--gemini-dir", home)
	}
	return args
}

// afterInitialize runs the separate server's sign-in bootstrap. `agy acp`
// reuses the CLI's own login, and its authenticate method could start a
// browser sign-in, so AO leaves it alone.
func afterInitialize(bootstrap authBootstrap) func(context.Context, *acpsdk.ClientSideConnection, acpsdk.InitializeResponse, acpdriver.LaunchConfig) error {
	return func(ctx context.Context, conn *acpsdk.ClientSideConnection, init acpsdk.InitializeResponse, cfg acpdriver.LaunchConfig) error {
		if isCLIServer(init) {
			return nil
		}
		return bootstrap.run(ctx, conn, init, cfg)
	}
}

// sessionModeForAgent maps AO's approvals onto `agy acp`'s modes. AO's auto
// modes take accept-edits, and permissionPolicy approves the commands it still
// asks about. The separate server keeps sessionMode.
func sessionModeForAgent(init acpsdk.InitializeResponse) func(ports.PermissionMode) string {
	if !isCLIServer(init) {
		return nil
	}
	return func(permission ports.PermissionMode) string {
		switch ports.NormalizePermissionMode(permission) {
		case ports.PermissionModeDefault:
			return modeDefault
		case ports.PermissionModeAcceptEdits, ports.PermissionModeAuto, ports.PermissionModeBypassPermissions:
			return cliModeAcceptEdits
		default:
			return ""
		}
	}
}

// permissionPolicy approves a request once under AO's auto modes, which is
// what the separate server's yolo mode does. `agy acp` has no such mode. The
// separate server in yolo never asks, so this changes nothing there.
func permissionPolicy(permission ports.PermissionMode, request acpsdk.RequestPermissionRequest) (acpsdk.PermissionOptionId, bool) {
	switch ports.NormalizePermissionMode(permission) {
	case ports.PermissionModeAuto, ports.PermissionModeBypassPermissions:
	default:
		return "", false
	}
	for _, option := range request.Options {
		if option.Kind == acpsdk.PermissionOptionKindAllowOnce {
			return option.OptionId, true
		}
	}
	return "", false
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
	var options []acpdriver.SessionOption
	if model := strings.TrimSpace(settings.Model); model != "" {
		options = append(options, acpdriver.SessionOption{ID: "model", Value: model, Optional: true})
	}
	// A startup fallback saves the ACP base model and its actual reasoning
	// level separately. Reapply that level when the corrected session resumes.
	if effort := strings.TrimSpace(settings.Effort); effort != "" {
		options = append(options, acpdriver.SessionOption{ID: thinkingLevelOption, Value: effort, Optional: true})
	}
	return options
}

// splitModelVariant turns gemini-3.8-flash-high into what server 1.4 and `agy
// acp` 1.5 offer: the gemini-3.8-flash model at thinking level high.
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
	return []acpsdk.ContentBlock{acpdriver.PlainStandingInstructionsBlock(standing)}
}
