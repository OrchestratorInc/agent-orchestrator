// Package zcode implements the ZCode agent adapter for the official OSS CLI
// (https://github.com/zai-org/ZCode, CLI source at apps/zcode-cli).
//
// ZCode is deliberately TUI-only in AO: the adapter launches the interactive
// `zcode` TUI and delivers worker prompts after startup. No chatdriver
// registry entry is registered for this harness, so per that registry's
// contract, chat mode fails with ports.ErrChatUnsupported — TUI sessions only.
//
// Only flags documented in the CLI's own reference are passed: `--cwd <path>`,
// `--mode plan|build|edit|yolo|guarded`, `--resume <sessionId>`, and
// `--disallowed-tools <names>`. Undocumented flags (`--model`,
// `--permission-mode`, `--print`, `--settings`, `--allowed-tools`) are never
// sent, so model/effort overrides and tool allowlists are rejected at launch
// rather than silently ignored.
//
// ZCode mints its own native session ids. This adapter installs no hooks, so
// GetRestoreCommand resumes the exact native id when session metadata carries
// one, and otherwise falls back to the documented `--continue`, which resumes
// the latest session for the workspace path — every AO worktree belongs to
// exactly one session, so within a session's own worktree that is its own
// latest native session.
package zcode

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	// adapterID is the registry id and the value users pass to
	// `ao spawn --agent`. It matches domain.HarnessZCode.
	adapterID = "zcode"
)

// Plugin is the ZCode agent adapter. It is safe for concurrent use; the
// binary path is resolved once and cached under binaryMu.
type Plugin struct {
	agentbase.Base
	binaryMu       sync.Mutex
	resolvedBinary string
}

// New returns a ready-to-register ZCode adapter.
func New() *Plugin {
	return &Plugin{}
}

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)
var _ ports.AgentAuthChecker = (*Plugin)(nil)
var _ ports.AgentExitDetector = (*Plugin)(nil)
var _ ports.AgentPromptReadinessProvider = (*Plugin)(nil)
var _ ports.AgentTerminalVersionRefresher = (*Plugin)(nil)

// ExitDetectionMode opts ZCode into AO's process supervisor. The TUI exposes
// no exit hook AO could observe, so the supervisor watches the CLI process.
func (*Plugin) ExitDetectionMode() ports.AgentExitDetectionMode {
	return ports.AgentExitDetectionSupervisor
}

// Manifest returns the adapter's static self-description.
func (*Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          adapterID,
		Name:        "ZCode",
		Description: "Run ZCode worker sessions in its interactive terminal TUI.",
		Version:     "0.0.1",
		Capabilities: []adapters.Capability{
			adapters.CapabilityAgent,
		},
	}
}

// GetConfigSpec reports the per-project agent config keys ZCode understands:
// a starting mode from the CLI's documented plan|build|edit|yolo|guarded
// vocabulary. No model key is declared: the CLI documents no --model flag and
// selects models through its own provider config.
func (*Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	if err := ctx.Err(); err != nil {
		return ports.ConfigSpec{}, err
	}
	return ports.ConfigSpec{
		Fields: []ports.ConfigField{
			{
				Key:         "mode",
				Type:        ports.ConfigFieldEnum,
				Description: "Starting permission mode passed to `zcode --mode`. Permissions overrides on an individual spawn take precedence.",
				Enum:        []string{"plan", "build", "edit", "yolo", "guarded"},
			},
		},
	}, nil
}

// GetLaunchCommand builds the argv to start an interactive ZCode session.
// Shape:
//
//	zcode [--cwd <WorkspacePath>] [--mode <mode>] [--disallowed-tools <names>]
//
// No --prompt is passed: AO keeps the interactive TUI and delivers worker
// task prompts after startup (see GetPromptDeliveryStrategy). Plain `zcode`
// with no arguments enters the TUI, which is exactly the session AO wants.
//
// <mode> comes from the spawn's permission override mapped onto ZCode's
// documented plan|build|edit|yolo|guarded modes, or from the project's
// configured "mode" key when no override is set. When neither applies, no
// --mode flag is emitted so the TUI resolves its own starting mode, exactly
// as a normal launch does.
//
// We intentionally do not pass a session id at launch: cfg.SessionID is the
// AO-internal id, not a ZCode-native session id. ZCode mints its own native
// id, which GetRestoreCommand resumes via --resume.
func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	// The CLI reference documents no --model/--effort/--settings flags, so an
	// override would be silently dropped by the parser. Refuse instead of
	// launching a session that ignores the project's configured model.
	if cfg.Config.Model != "" || cfg.Config.Effort != "" {
		return nil, fmt.Errorf("zcode: model/effort overrides are unsupported; the CLI documents no such flags")
	}
	// --allowed-tools is undocumented; refusing keeps the reviewer tool
	// guarantee honest instead of launching with the restriction dropped.
	if len(cfg.AllowedTools) > 0 {
		return nil, fmt.Errorf("zcode: tool allowlists are unsupported; the CLI documents no --allowed-tools flag")
	}

	binary, err := p.zcodeBinary(ctx)
	if err != nil {
		return nil, err
	}

	cmd := []string{binary}

	if cfg.WorkspacePath != "" {
		cmd = append(cmd, "--cwd", cfg.WorkspacePath)
	}

	// The spawn's permission override wins; the project's configured mode key
	// applies when no override is set. The mode value is validated against the
	// CLI's documented vocabulary by AgentConfig.Validate on write.
	mode := zcodePermissionMode(cfg.Permissions)
	if mode == "" {
		mode = strings.TrimSpace(cfg.Config.Mode)
	}
	if mode != "" {
		cmd = append(cmd, "--mode", mode)
	}

	// --disallowed-tools is documented for whole-tool removals, comma- or
	// space-separated; commas keep it one argv element.
	if len(cfg.DisallowedTools) > 0 {
		cmd = append(cmd, "--disallowed-tools", strings.Join(cfg.DisallowedTools, ","))
	}

	return cmd, nil
}

// zcodePermissionMode maps an AO permission mode onto ZCode's documented
// --mode vocabulary. Unknown or default modes return "" so the TUI keeps its
// own starting mode.
func zcodePermissionMode(mode ports.PermissionMode) string {
	switch mode {
	case ports.PermissionModeAcceptEdits:
		return "edit"
	case ports.PermissionModeAuto:
		return "build"
	case ports.PermissionModeBypassPermissions:
		return "yolo"
	default:
		return ""
	}
}

// GetPromptDeliveryStrategy reports that prompted sessions receive their task
// after the interactive ZCode TUI starts.
func (p *Plugin) GetPromptDeliveryStrategy(ctx context.Context, cfg ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if cfg.Prompt != "" {
		return ports.PromptDeliveryAfterStart, nil
	}
	return ports.PromptDeliveryInCommand, nil
}

// PromptReadinessHints waits for ZCode's composer before AO injects the
// worker's first task. The composer placeholder ("Ask a task about this
// workspace", rendered by the client's own TUI package) is the ready marker:
// the TUI keeps booting for well over a second after the process starts
// (MCP servers connect, then the composer renders), and keystrokes typed
// before it is live are lost — observed live as an echoed prompt line
// followed by an empty composer and a session that never ran. The timeout
// fallback still bounds the wait: a build that drops the placeholder
// degrades to the old fixed-delay behavior instead of blocking spawn.
func (*Plugin) PromptReadinessHints(ctx context.Context, _ ports.LaunchConfig) (ports.PromptReadinessHints, error) {
	if err := ctx.Err(); err != nil {
		return ports.PromptReadinessHints{}, err
	}
	return ports.PromptReadinessHints{
		InitialDelay: 500 * time.Millisecond,
		Patterns:     []string{"Ask a task about this workspace"},
		Timeout:      30 * time.Second,
		PollInterval: 100 * time.Millisecond,
		Lines:        100,
	}, nil
}

// GetRestoreCommand rebuilds the argv that continues an existing ZCode
// session: `zcode [--cwd <WorkspacePath>] [--mode <mode>] (--resume <id> |
// --continue)`. The CLI reference requires the same effective working
// directory on resume, so --cwd is re-applied. An exact native id from
// session metadata wins; otherwise --continue resumes the latest session for
// that same working directory, which for a dedicated AO worktree is this
// session's own latest native session (the CLI documents the two flags as
// mutually exclusive). ok is false only when neither is available, so the
// caller fresh-spawns. The resume-time prompt is left to AO's after-start
// delivery, matching the launch path.
func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) (cmd []string, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	agentSessionID := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if agentSessionID == "" && cfg.Session.WorkspacePath == "" {
		return nil, false, nil
	}

	binary, err := p.zcodeBinary(ctx)
	if err != nil {
		return nil, false, err
	}

	cmd = []string{binary}

	if cfg.Session.WorkspacePath != "" {
		cmd = append(cmd, "--cwd", cfg.Session.WorkspacePath)
	}

	mode := zcodePermissionMode(cfg.Permissions)
	if mode == "" {
		mode = strings.TrimSpace(cfg.Config.Mode)
	}
	if mode != "" {
		cmd = append(cmd, "--mode", mode)
	}

	if agentSessionID != "" {
		cmd = append(cmd, "--resume", agentSessionID)
	} else {
		cmd = append(cmd, "--continue")
	}
	return cmd, true, nil
}

// SessionInfo surfaces the session metadata AO has captured for this session
// (native session id, title, summary). This adapter installs no hooks, so
// ok=false until another surface records metadata.
func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(session)
	return info, ok, nil
}

// SupportsTerminalVersionRefresh reports that ZCode does not opt into
// exact-conversation idle restarts yet: restore falls back to --continue
// (latest session for the worktree) when no native id was captured, which
// cannot guarantee the same conversation a restart would need to reselect.
func (*Plugin) SupportsTerminalVersionRefresh() bool { return false }

// ResolveBinary resolves the executable path for the plugin, so readiness
// install checks and the agent pickers see ZCode like any other harness.
func (p *Plugin) ResolveBinary(ctx context.Context) (string, error) {
	return p.zcodeBinary(ctx)
}

var zcodeBinarySpec = binaryutil.BinarySpec{
	Label:     "zcode",
	Names:     []string{"zcode"},
	WinNames:  []string{"zcode.exe", "zcode.cmd", "zcode"},
	UnixPaths: []string{"/usr/local/bin/zcode", "/opt/homebrew/bin/zcode"},
	// The official CLI ships as an npm package (@zcode/cli, bin "zcode"), so
	// nvm/Volta/fnm version-managed global bins are valid install locations.
	NodeManaged: true,
}

// ResolveZCodeBinary returns the path to the zcode binary on this machine,
// searching PATH then a handful of well-known install locations. It returns a
// wrapped ports.ErrAgentBinaryNotFound when zcode is absent.
func ResolveZCodeBinary(ctx context.Context) (string, error) {
	return binaryutil.ResolveBinary(ctx, zcodeBinarySpec)
}

func (p *Plugin) zcodeBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}

	binary, err := ResolveZCodeBinary(ctx)
	if err != nil {
		return "", err
	}
	p.resolvedBinary = binary
	return binary, nil
}

// InvalidateBinaryResolution forgets the cached executable path, so the next
// launch resolves it again: after AO installs a managed copy, for example.
func (p *Plugin) InvalidateBinaryResolution() {
	p.binaryMu.Lock()
	p.resolvedBinary = ""
	p.binaryMu.Unlock()
}
