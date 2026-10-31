// Package qodercli implements the Qoder CLI terminal adapter: launching an
// interactive worker inside a session worktree, resuming it by native id, and
// reading back the session metadata its hooks report.
//
// Qoder CLI ("qodercli") is a gemini-cli fork whose command surface mirrors
// Claude Code: --session-id pins a native UUID, --resume continues one,
// --permission-mode and --allowed-tools/--disallowed-tools scope approvals,
// --append-system-prompt[-file] extends the system prompt, and its hook events,
// payload fields and notification vocabulary are Claude Code's. Activity
// derivation therefore delegates to claudecode.DeriveActivityState.
//
// Three things differ from the Claude Code adapter:
//
//   - The initial task rides on -i rather than a positional argument: a bare
//     one-word prompt close to a subcommand name is rejected as an unknown
//     command.
//   - Hooks, workspace trust and the auto-update switch travel in one AO-owned
//     --settings file under AO's data dir, so nothing is written into the
//     checkout (see settings.go).
//   - Tool scoping is one --allowed-tools flag per rule, where Claude Code takes
//     a single comma-joined value.
package qodercli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	// adapterID is the registry id, the domain.AgentHarness value, and the
	// value users pass to `ao spawn --agent`.
	adapterID = "qodercli"
)

// qodercliSessionNamespace derives a stable native session UUID from an AO
// session id. It is deliberately not Claude Code's namespace: one AO session
// may drive both harnesses over its lifetime and the ids must not collide.
var qodercliSessionNamespace = uuid.MustParse("6b1f0a34-2f6d-4c58-9d7e-2a0c9e5b7d61")

// Plugin is the Qoder CLI agent adapter. It is safe for concurrent use; the
// resolved binary path is cached under binaryMu.
type Plugin struct {
	agentbase.Base
	binaryMu       sync.Mutex
	resolvedBinary string
}

// New returns a ready-to-register Qoder CLI adapter.
func New() *Plugin { return &Plugin{} }

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)
var _ ports.AgentAuthChecker = (*Plugin)(nil)
var _ ports.AgentBinaryResolver = (*Plugin)(nil)
var _ ports.AgentBinaryResolutionInvalidator = (*Plugin)(nil)
var _ ports.AgentTerminalVersionRefresher = (*Plugin)(nil)

// Manifest returns the adapter's static self-description.
func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          adapterID,
		Name:        "Qoder CLI",
		Description: "Run Qoder CLI worker sessions.",
		Version:     "0.0.1",
		Capabilities: []adapters.Capability{
			adapters.CapabilityAgent,
		},
	}
}

// permissionConfigEnum lists the permission modes the "permissions" config key
// accepts, mirroring the ports.PermissionMode constants.
var permissionConfigEnum = []string{
	string(ports.PermissionModeDefault),
	string(ports.PermissionModeAcceptEdits),
	string(ports.PermissionModeAuto),
	string(ports.PermissionModeBypassPermissions),
}

// GetConfigSpec reports the per-project agent config keys Qoder CLI understands.
func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	if err := ctx.Err(); err != nil {
		return ports.ConfigSpec{}, err
	}
	return ports.ConfigSpec{
		Fields: []ports.ConfigField{
			{
				Key:         "model",
				Type:        ports.ConfigFieldString,
				Description: "Model override passed to `qodercli --model`.",
			},
			{
				Key:         "permissions",
				Type:        ports.ConfigFieldEnum,
				Description: "Starting permission mode.",
				Enum:        permissionConfigEnum,
			},
		},
	}, nil
}

// GetLaunchCommand builds the argv to start an interactive Qoder CLI session:
//
//	qodercli --session-id <uuid> [--permission-mode <mode>]
//	         [--allowed-tools <rule>]... [--disallowed-tools <rule>]...
//	         [--model <m>] [--append-system-prompt-file <f> | --append-system-prompt <t>]
//	         --settings <path> [-i <task>]
//
// --session-id pins the native UUID so GetRestoreCommand can rebuild
// `qodercli --resume <uuid>` later. AO's "default" permission mode emits no
// flag, leaving the starting mode to the user's own Qoder CLI config.
func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	binary, err := p.qodercliBinary(ctx)
	if err != nil {
		return nil, err
	}

	nativeID, err := launchSessionUUID(cfg)
	if err != nil {
		return nil, err
	}

	// An explicit LaunchConfig.Permissions wins; otherwise the project's
	// configured permissions drive the starting mode.
	permissions := cfg.Permissions
	if permissions == "" {
		permissions = cfg.Config.Permissions
	}

	cmd := []string{binary}
	if nativeID != "" {
		cmd = append(cmd, "--session-id", nativeID)
	}
	cmd = appendPermissionFlag(cmd, permissions)
	cmd = appendToolFlags(cmd, cfg.AllowedTools, cfg.DisallowedTools)
	agentbase.AppendModelFlag(&cmd, cfg.Config, "--model")
	if cmd, err = appendSystemPrompt(cmd, cfg.SystemPromptFile, cfg.SystemPrompt); err != nil {
		return nil, err
	}

	settings, err := SettingsArg(cfg.DataDir, cfg.SessionID, cfg.WorkspacePath)
	if err != nil {
		return nil, err
	}
	cmd = append(cmd, "--settings", settings)

	if prompt := strings.TrimSpace(cfg.Prompt); prompt != "" {
		cmd = append(cmd, "-i", prompt)
	}
	return cmd, nil
}

// GetPromptDeliveryStrategy reports that the task is carried on -i, so AO does
// not also type it into the pane.
func (p *Plugin) GetPromptDeliveryStrategy(ctx context.Context, _ ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return ports.PromptDeliveryInCommand, nil
}

// GetRestoreCommand rebuilds the argv that continues an existing Qoder CLI
// session: `qodercli [flags] --settings <path> --resume <id>`. It prefers the
// hook-captured native session id and falls back to the deterministic UUID AO
// pinned at launch. ok is false when neither is usable, so the caller starts a
// fresh conversation instead.
//
// --session-id is never combined with --resume: Qoder CLI rejects that pairing
// unless --fork-session is also passed.
func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) (cmd []string, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}

	agentSessionID := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if agentSessionID == "" && cfg.Session.ID != "" {
		agentSessionID = SessionUUID(cfg.Session.ID)
	}
	// A non-UUID resume argument is read as an index into the recent-session
	// list ("--resume 1" means the most recent session), which would attach this
	// pane to an unrelated conversation. Only ever resume a real UUID.
	if !isUUID(agentSessionID) {
		return nil, false, nil
	}

	binary, err := p.qodercliBinary(ctx)
	if err != nil {
		return nil, false, err
	}

	cmd = []string{binary}
	cmd = appendPermissionFlag(cmd, cfg.Permissions)
	cmd = appendToolFlags(cmd, cfg.AllowedTools, cfg.DisallowedTools)
	agentbase.AppendModelFlag(&cmd, cfg.Config, "--model")
	if cmd, err = appendSystemPrompt(cmd, cfg.SystemPromptFile, cfg.SystemPrompt); err != nil {
		return nil, false, err
	}

	settings, err := SettingsArg(cfg.DataDir, cfg.Session.ID, cfg.Session.WorkspacePath)
	if err != nil {
		return nil, false, err
	}
	cmd = append(cmd, "--settings", settings, "--resume", agentSessionID)

	// cfg.Prompt is deliberately not put on the resume command line: the TUI
	// resolves --resume asynchronously, so a command-line turn can be submitted
	// into a conversation that has not finished loading. The turn reaches the
	// pane after startup instead.
	return cmd, true, nil
}

// SessionInfo surfaces the normalized session metadata Qoder CLI's hooks
// persisted into AO's store. It reads only session.Metadata, never transcripts.
func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(session)
	return info, ok, nil
}

// SupportsTerminalVersionRefresh reports that AO does not move a running Qoder
// CLI session between binaries: the CLI reports no version on its own hook
// surface, so a mid-session upgrade could not be proven to have happened.
func (p *Plugin) SupportsTerminalVersionRefresh() bool { return false }

// launchSessionUUID resolves the native UUID a fresh launch pins: the id AO
// supplied when it reserved one, otherwise the deterministic id derived from the
// AO session.
func launchSessionUUID(cfg ports.LaunchConfig) (string, error) {
	if native := strings.TrimSpace(cfg.NativeSessionID); native != "" {
		parsed, err := uuid.Parse(native)
		if err != nil {
			return "", fmt.Errorf("qodercli: invalid native session id: %w", err)
		}
		return parsed.String(), nil
	}
	if cfg.SessionID != "" {
		return SessionUUID(cfg.SessionID), nil
	}
	return "", nil
}

// SessionUUID maps an AO session id onto the native Qoder CLI session UUID used
// by --session-id and --resume.
func SessionUUID(aoSessionID string) string {
	return uuid.NewSHA1(qodercliSessionNamespace, []byte(aoSessionID)).String()
}

func isUUID(value string) bool {
	trimmed := strings.TrimSpace(value)
	_, err := uuid.Parse(trimmed)
	return err == nil && trimmed != ""
}

// appendPermissionFlag maps AO's permission modes onto Qoder CLI's
// --permission-mode choices. Default emits no flag.
func appendPermissionFlag(cmd []string, permissions ports.PermissionMode) []string {
	switch ports.NormalizePermissionMode(permissions) {
	case ports.PermissionModeAcceptEdits:
		return append(cmd, "--permission-mode", "accept_edits")
	case ports.PermissionModeAuto:
		return append(cmd, "--permission-mode", "auto")
	case ports.PermissionModeBypassPermissions:
		return append(cmd, "--permission-mode", "bypass_permissions")
	default:
		return cmd
	}
}

// appendToolFlags scopes a session to a tool allowlist/denylist. Qoder CLI takes
// one rule per flag occurrence, unlike Claude Code's comma-joined value.
func appendToolFlags(cmd, allowed, disallowed []string) []string {
	for _, rule := range allowed {
		if rule = strings.TrimSpace(rule); rule != "" {
			cmd = append(cmd, "--allowed-tools", rule)
		}
	}
	for _, rule := range disallowed {
		if rule = strings.TrimSpace(rule); rule != "" {
			cmd = append(cmd, "--disallowed-tools", rule)
		}
	}
	return cmd
}

// appendSystemPrompt prefers AO's prompt file so standing instructions stay out
// of the terminal stream, falling back to the inline text.
func appendSystemPrompt(cmd []string, promptFile, prompt string) ([]string, error) {
	if promptFile != "" {
		info, err := os.Stat(promptFile)
		if err != nil {
			return nil, fmt.Errorf("qodercli: inspect system prompt file: %w", err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("qodercli: system prompt file %q is not a regular file", promptFile)
		}
		return append(cmd, "--append-system-prompt-file", promptFile), nil
	}
	if prompt != "" {
		cmd = append(cmd, "--append-system-prompt", prompt)
	}
	return cmd, nil
}

// qodercliBinarySpec locates the qodercli binary: PATH first, then the
// standalone installer's symlink, npm global locations, and Homebrew. The
// `qoder` binary is deliberately not a candidate: it is an IDE dispatcher script
// that only forwards some invocations to qodercli.
var qodercliBinarySpec = binaryutil.BinarySpec{
	Label:         "qodercli",
	Names:         []string{"qodercli"},
	WinNames:      []string{"qodercli.cmd", "qodercli.exe", "qodercli"},
	UnixPaths:     []string{"/usr/local/bin/qodercli", "/opt/homebrew/bin/qodercli"},
	UnixHomePaths: binaryutil.NodeManagedUnixHomePaths("qodercli"),
	NodeManaged:   true,
	WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "qodercli.cmd"}},
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "qodercli.exe"}},
	},
}

// ResolveQodercliBinary returns the path to the qodercli binary, or a wrapped
// ports.ErrAgentBinaryNotFound when it is absent.
func ResolveQodercliBinary(ctx context.Context) (string, error) {
	return binaryutil.ResolveBinary(ctx, qodercliBinarySpec)
}

// ResolveBinary reports the resolved qodercli path without launching it.
func (p *Plugin) ResolveBinary(ctx context.Context) (string, error) {
	return p.qodercliBinary(ctx)
}

// InvalidateBinaryResolution drops the cached path so the next call observes a
// fresh install.
func (p *Plugin) InvalidateBinaryResolution() {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()
	p.resolvedBinary = ""
}

func (p *Plugin) qodercliBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}
	binary, err := ResolveQodercliBinary(ctx)
	if err != nil {
		return "", err
	}
	p.resolvedBinary = binary
	return binary, nil
}
