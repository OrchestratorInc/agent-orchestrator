// Package hermes implements AO's Hermes Agent adapter: launching new
// interactive sessions, resuming hook-tracked sessions, and probing local
// installation, identity, and authentication.
//
// Hermes (binary "hermes") is launched as `hermes chat -q "<prompt>"`. On a
// real TTY — which an AO terminal pane always is — the query seeds an
// interactive session as its first submitted turn, so AO delivers the prompt in
// the launch command rather than typing it after startup. Hermes records AO's
// sessions under the `tool` source tag so they stay out of the user's personal
// session lists, the same convention its own protocol integrations follow with
// the `acp` source.
//
// Permission/approval for worker terminals is controlled by the `--yolo` flag
// (bypass all dangerous-command approval prompts); Hermes has no CLI flag for
// its intermediate approval modes, so every other AO permission mode emits no
// flag and Hermes' own approvals configuration governs.
//
// Hermes has no workspace-local hook surface — its shell hooks live in the
// user's global `~/.hermes/config.yaml` — so AO installs no activity hooks and
// derives no hook-based session identity for terminal sessions. Structured Chat
// sessions run through the native `hermes acp` server instead, where the
// protocol itself provides turn, approval, and resume events.
package hermes

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"

	_ "modernc.org/sqlite" // register sqlite driver for Hermes state probes
)

const adapterID = "hermes"

// hermesIdentityProbeTimeout bounds the adapter-owned --help identity probe.
var hermesIdentityProbeTimeout = time.Second

// hermesIdentityProbeWaitDelay bounds the pipe-drain window left when a
// misbehaving candidate holds stdout/stderr open after exiting.
const hermesIdentityProbeWaitDelay = 100 * time.Millisecond

// hermesIdentityCommand is kept injectable so identity tests never need to
// launch a real CLI.
var hermesIdentityCommand = runHermesIdentityCommand

func runHermesIdentityCommand(ctx context.Context, binary string, args ...string) ([]byte, error) {
	cmd := aoprocess.CommandContext(ctx, binary, args...) //nolint:gosec // binary is resolved by binaryutil; args are static
	cmd.WaitDelay = hermesIdentityProbeWaitDelay
	return cmd.CombinedOutput()
}

// Plugin is the Hermes agent adapter. It is safe for concurrent use; binary
// resolution is cached after the first successful lookup.
type Plugin struct {
	agentbase.Base
	binaryMu       sync.Mutex
	resolvedBinary string
}

// New returns a ready-to-register Hermes adapter.
func New() *Plugin {
	return &Plugin{}
}

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)
var _ ports.AgentAuthChecker = (*Plugin)(nil)
var _ ports.AgentBinaryResolver = (*Plugin)(nil)
var _ ports.AgentInterfaceHandoffHistoryProbe = (*Plugin)(nil)

// Manifest returns the adapter's static self-description.
func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          adapterID,
		Name:        "Hermes",
		Description: "Run Hermes Agent worker sessions.",
		Version:     "0.0.1",
		Capabilities: []adapters.Capability{
			adapters.CapabilityAgent,
		},
	}
}

// GetConfigSpec reports the per-project agent config keys Hermes understands.
func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx, "Model override passed to `hermes chat -m` (e.g. provider/model).")
}

// GetLaunchCommand builds the argv to start a fresh interactive Hermes session:
//
//	[env HERMES_EPHEMERAL_SYSTEM_PROMPT=<text>] hermes chat [--source tool]
//	[-m <model>] [--yolo] [-q <prompt>]
//
// The system instructions ride in an env var because Hermes has no
// system-prompt flag; HERMES_EPHEMERAL_SYSTEM_PROMPT is injected at API-call
// time and never persisted into the session transcript. The query is passed via
// -q, which seeds the interactive session's first turn on a TTY.
func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	binary, err := p.hermesBinary(ctx)
	if err != nil {
		return nil, err
	}
	systemPrompt, err := systemPromptText(cfg.SystemPrompt, cfg.SystemPromptFile)
	if err != nil {
		return nil, err
	}
	return buildChatCommand(binary, chatOptions{
		SystemPrompt: systemPrompt,
		Model:        strings.TrimSpace(cfg.Config.Model),
		Bypass:       ports.NormalizePermissionMode(cfg.Permissions) == ports.PermissionModeBypassPermissions,
		Prompt:       strings.TrimSpace(cfg.Prompt),
	}), nil
}

// GetRestoreCommand continues a Hermes session when AO has captured its native
// session id. ok=false means the session manager should fall back to a fresh
// launch.
func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	agentSessionID := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if agentSessionID == "" {
		return nil, false, nil
	}

	binary, err := p.hermesBinary(ctx)
	if err != nil {
		return nil, false, err
	}
	systemPrompt, err := systemPromptText(cfg.SystemPrompt, cfg.SystemPromptFile)
	if err != nil {
		return nil, false, err
	}
	return buildChatCommand(binary, chatOptions{
		SystemPrompt: systemPrompt,
		Model:        strings.TrimSpace(cfg.Config.Model),
		Bypass:       ports.NormalizePermissionMode(cfg.Permissions) == ports.PermissionModeBypassPermissions,
		Prompt:       strings.TrimSpace(cfg.Prompt),
		Resume:       agentSessionID,
	}), true, nil
}

// SessionInfo surfaces metadata captured by AO's generic session machinery.
func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(session)
	return info, ok, nil
}

// NativeConversationExists reports whether Hermes has a persisted session row
// for id in its state database, so an interface transition can tell a resumable
// conversation from a declared-but-never-persisted one.
func (p *Plugin) NativeConversationExists(ctx context.Context, _ ports.SessionRef, nativeConversationID string, env map[string]string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	id := strings.TrimSpace(nativeConversationID)
	if id == "" {
		return false, nil
	}
	home, ok := hermesHome(env)
	if !ok {
		return false, nil
	}
	dbPath := filepath.Join(home, "state.db")
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	db, err := sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		return false, err
	}
	defer func() { _ = db.Close() }()

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions WHERE id = ?`, id).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

// hermesHome resolves the Hermes home directory, honoring HERMES_HOME the same
// way the Hermes CLI does. ok is false when no home directory can be resolved.
func hermesHome(env map[string]string) (string, bool) {
	if home := strings.TrimSpace(env["HERMES_HOME"]); home != "" {
		return home, true
	}
	if home := strings.TrimSpace(os.Getenv("HERMES_HOME")); home != "" {
		return home, true
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "", false
	}
	return filepath.Join(home, ".hermes"), true
}

// chatOptions carries the resolved launch inputs shared by GetLaunchCommand and
// GetRestoreCommand.
type chatOptions struct {
	SystemPrompt string
	Model        string
	Bypass       bool
	Prompt       string
	Resume       string
}

// buildChatCommand renders the `hermes chat` argv. A non-empty system prompt is
// delivered through an `env VAR=<text>` argv prefix because the runtime runs
// the argv through a shell, which execs `env`, which sets the var and execs
// hermes — the same technique the goose adapter uses for GOOSE_MODE. A bare
// `VAR=...` argv element would be shell-quoted into a command name rather than
// read as an assignment.
func buildChatCommand(binary string, opts chatOptions) []string {
	var cmd []string
	if opts.SystemPrompt != "" {
		cmd = append(cmd, "env", "HERMES_EPHEMERAL_SYSTEM_PROMPT="+opts.SystemPrompt)
	}
	cmd = append(cmd, binary, "chat", "--source", "tool")
	if opts.Resume != "" {
		cmd = append(cmd, "--resume", opts.Resume)
	}
	if opts.Model != "" {
		cmd = append(cmd, "-m", opts.Model)
	}
	if opts.Bypass {
		cmd = append(cmd, "--yolo")
	}
	if opts.Prompt != "" {
		cmd = append(cmd, "-q", opts.Prompt)
	}
	return cmd
}

// systemPromptText returns the system instructions to inject. The env var takes
// inline text only, so a system-prompt file is read from disk when inline
// instructions are unavailable.
func systemPromptText(inline, file string) (string, error) {
	if inline != "" {
		return inline, nil
	}
	if file == "" {
		return "", nil
	}
	data, err := os.ReadFile(file) //nolint:gosec // path is AO-owned launch config
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

var hermesBinarySpec = binaryutil.BinarySpec{
	Label:     "hermes",
	Names:     []string{"hermes"},
	WinNames:  []string{"hermes.exe", "hermes.cmd", "hermes"},
	UnixPaths: []string{"/usr/local/bin/hermes", "/opt/homebrew/bin/hermes"},
	// The supported installers link the launcher into ~/.local/bin and keep a
	// repo-local bin dir inside the checkout; Hermes is Python-based, so the
	// Node version-manager fallbacks do not apply.
	UnixHomePaths: [][]string{
		{".local", "bin", "hermes"},
		{".hermes", "bin", "hermes"},
	},
	WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinHome, Parts: []string{".hermes", "bin", "hermes.exe"}},
		{Base: binaryutil.WinHome, Parts: []string{".hermes", "hermes-agent", "venv", "Scripts", "hermes.exe"}},
	},
	ValidateIdentity: isOfficialHermesBinary,
}

// ResolveHermesBinary finds the `hermes` binary, searching PATH then common
// install locations. It returns a wrapped ports.ErrAgentBinaryNotFound when no
// candidate passes identity validation.
func ResolveHermesBinary(ctx context.Context) (string, error) {
	return binaryutil.ResolveBinary(ctx, hermesBinarySpec)
}

func (p *Plugin) hermesBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}
	binary, err := ResolveHermesBinary(ctx)
	if err != nil {
		return "", err
	}
	p.resolvedBinary = binary
	return binary, nil
}

// isOfficialHermesBinary confirms a name-matching executable is the Hermes
// Agent CLI by checking its help banner, the same approach the goose adapter
// uses to reject similarly named unrelated CLIs.
func isOfficialHermesBinary(ctx context.Context, binary string) bool {
	if err := ctx.Err(); err != nil || binary == "" {
		return false
	}
	probeCtx, cancel := context.WithTimeout(ctx, hermesIdentityProbeTimeout)
	defer cancel()
	out, err := hermesIdentityCommand(probeCtx, binary, "--help")
	if probeCtx.Err() != nil || err != nil {
		return false
	}
	return strings.Contains(string(out), "Hermes Agent")
}
