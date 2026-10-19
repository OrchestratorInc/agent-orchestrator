// Package zcode implements the ZCode agent adapter: launching new one-shot
// ZCode sessions and resuming the latest native session for a worktree.
//
// ZCode (Z.AI's coding CLI, binary "zcode") is driven one-shot:
// `zcode -p <prompt> --cwd <worktree>`. The reply streams readably on the pane
// and the process exits, leaving AO's keep-alive shell in place for inspection.
// ZCode 0.16.9 has no TUI initial-prompt flag and no --model, --session-id, or
// --append-system-prompt CLI surface (verified via `zcode --help`), which
// shapes the adapter:
//
//   - Standing instructions (the orchestrator/worker role text) are PREPENDED
//     to the -p prompt text. ZCode has no system-prompt flag, so the prepended
//     text becomes the native session's first user turn — the only channel
//     that persists the role — and therefore survives every later -c/--resume
//     restore with no flag re-application (unlike Claude Code's
//     --append-system-prompt).
//   - cfg.Config.Model is validated but forwarded nowhere: zcode has no model
//     CLI flag or documented environment variable. The effective model comes
//     from defaultModelSelection in $ZCODE_PERSONAL_PROVIDER_CONFIG_FILE
//     (~/.zcode/v2/provider_config.json) and is switchable per session with
//     /model in the TUI. For the same reason the adapter exposes no "model"
//     config field; its one config key is "mode" (see GetConfigSpec).
//
// Restore is cwd-scoped `-c` ("resume the latest session for the current
// directory", per `zcode --help`) — deterministic because each AO session owns
// its own worktree. `--resume <sess_...>` is kept as a vestigial upgrade branch
// nothing populates at HEAD. Both restore shapes re-apply the --mode flags the
// launch pinned (see appendModeFlags): a restore must not silently drop a
// session back onto zcode's flagless yolo default.
//
// There is no workspace hook installer in v1 and no trust PreLaunch. Zcode's
// hooks are configurable through the PLUGIN system — a per-plugin
// hooks/hooks.json manifest (verified in the zcode bundle) — not a workspace
// settings file, and the bundle carries no trust dialog.
//
// Permissions: zcode's --mode accepts build, edit, plan, yolo, or guarded,
// with a documented default of yolo for --prompt runs (all approvals skipped).
// Because of that, AO's default/unset mode is PINNED to --mode edit rather
// than emitting no flag (see appendModeFlags); inheriting yolo on every
// untouched config would spawn all workers with approvals skipped.
//
// Status (activity): no hooks are installed in v1, so zcode is deliberately
// absent from activitydispatch.Derivers (SupportsHarness("zcode") is false)
// and the dashboard treats silence as idle. The inverse also holds and is
// worth stating plainly: with no hook signals, ACTIVE work also reads as idle
// (StatusWorking requires an ActivityActive hook callback), waiting_input is
// unreachable, and the dashboard cannot distinguish a running zcode turn from
// a finished one sitting in the keep-alive shell. The reaper does see the
// one-shot process end: its workload probe (containsManagedWorkload) notices
// `zcode -p` has left the pane and flips the row to exited within one 5 s
// tick, even though the keep-alive shell keeps tmux has-session true. A
// future Tier-A installer ships a zcode plugin whose hooks.json runs
// `ao hooks zcode <event>` per event and registers zcode.DeriveActivityState
// in activitydispatch.
//
// Messages (ao send): the orchestrator's standing instructions tell workers
// that `ao send` is the message channel, and AO delivers those messages by
// typing them at the session pane's foreground process (tmux send-keys +
// Enter). The launch (GetLaunchCommand) puts nothing interactive there: while
// `zcode -p` runs the text would go to zcode's stdin (undefined), and in the
// few seconds between -p returning and the reaper's next tick it would be
// typed at the keep-alive shell prompt and EXECUTED as a shell command in the
// worktree. The adapter therefore declares one-shot terminal messaging (see
// messaging.go), and the session manager refuses `ao send` to a zcode TUI
// session up front with HARNESS_ONE_SHOT, naming the harness and pointing at
// `ao spawn`. The declaration is per harness, not per generation: a restore
// (GetRestoreCommand, `zcode -c`, no -p) opens the full-screen TUI, which
// could read a pasted follow-up, but the session record carries no fact that
// tells that generation apart from the one-shot launch, so `ao send` is
// refused there too; type into the pane directly instead. The mapped
// app-server chat driver runs in chat mode, which is relayed through its
// conversation and never gated.
package zcode

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	// adapterID is the registry id and the value users pass to
	// `ao spawn --harness`. It matches domain.HarnessZcode.
	adapterID = "zcode"
)

// zcodeBinarySpec resolves the `zcode` CLI. The ZCode desktop app installs no
// CLI launcher, so the expected install is a user-provided sh wrapper at
// ~/.local/bin/zcode (docs/harnesses/zcode.md carries it) that exports
// ZCODE_BASE_URL, the builtin and personal provider config file paths, and the
// isolated ZCODE_DATA_BASE_DIR credential store before exec'ing node on the
// app-bundled zcode.cjs — resolving that wrapper (never a bare .cjs) is
// required: without the wrapper's environment a bare bundle invocation fails
// to reach the provider.
// The spec probes exact executable paths only, so a bundled zcode.cjs can
// never match. zcode is an app-bundled CLI rather than an npm global, so
// NodeManaged version-manager fallbacks are deliberately not included.
var zcodeBinarySpec = binaryutil.BinarySpec{
	Label:     "zcode",
	Names:     []string{"zcode"},
	WinNames:  []string{"zcode.cmd", "zcode.exe", "zcode"},
	UnixPaths: []string{"/usr/local/bin/zcode", "/opt/homebrew/bin/zcode"},
	UnixHomePaths: [][]string{
		{".local", "bin", "zcode"},
	},
	WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinHome, Parts: []string{".local", "bin", "zcode.exe"}},
		{Base: binaryutil.WinHome, Parts: []string{".local", "bin", "zcode.cmd"}},
	},
}

// Plugin is the ZCode agent adapter. It is safe for concurrent use; the binary
// path is resolved once and cached under binaryMu.
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

// Manifest returns the adapter's static self-description.
func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          adapterID,
		Name:        "ZCode",
		Description: "Run ZCode worker sessions.",
		Version:     "0.0.1",
		Capabilities: []adapters.Capability{
			adapters.CapabilityAgent,
		},
	}
}

// permissionConfigEnum lists the permission modes the "mode" config key
// accepts. It mirrors the ports.PermissionMode constants so a project's stored
// config validates against the same vocabulary the launch command maps.
var permissionConfigEnum = []string{
	string(ports.PermissionModeDefault),
	string(ports.PermissionModeAcceptEdits),
	string(ports.PermissionModeAuto),
	string(ports.PermissionModeBypassPermissions),
}

// GetConfigSpec reports the per-project agent config key ZCode understands: a
// starting permission mode. The field is keyed "mode" — the vocabulary every
// harness must expose as "model" or "mode" — and its value is stored in the
// typed AgentConfig.Permissions slot, the only config field whose vocabulary
// this enum validates against. There is deliberately no "model" field: zcode
// has no model CLI flag or environment variable, and the protocol rejects a
// model parameter outright, so a stored model override could never be honored.
func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	if err := ctx.Err(); err != nil {
		return ports.ConfigSpec{}, err
	}
	return ports.ConfigSpec{
		Fields: []ports.ConfigField{
			{
				Key:  "mode",
				Type: ports.ConfigFieldEnum,
				Description: "Starting permission mode. default (or unset) pins --mode edit — the same " +
					"mode an explicit accept-edits spawn gets — instead of emitting no flag, because a " +
					"flagless -p run inherits ZCode's own documented default of yolo (all approvals " +
					"skipped). auto maps to build; bypass-permissions maps to yolo.",
				Enum:    permissionConfigEnum,
				Default: string(ports.PermissionModeAcceptEdits),
			},
		},
	}, nil
}

// GetLaunchCommand builds the argv to start a new ZCode session:
//
//	zcode [--cwd <worktree>] [--mode <mode>] [-p <standing instructions + task>]
//
// The prompt branch is the heart of the adapter. Standing instructions (the
// orchestrator/worker role) cannot go through a system-prompt flag — zcode has
// none — so they are prepended to the task prompt as the -p value. When the
// instructions are non-empty they ALWAYS force a -p run, including an
// instructions-only first turn when the task prompt is empty: that turn
// persists the role into the native session, where restore then retains it.
// Note the AO-level consequence: the manager always derives a non-empty system
// prompt for workers and orchestrators (buildSystemPrompt), so through AO the
// "plain interactive TUI" branch below is dead code and a promptless spawn
// DOES auto-run a kickoff turn of role text — deliberately contradicting the
// manager's documented promptless-spawn intent ("a promptless spawn delivers
// no user prompt at all") because the first turn is the only channel that
// persists the role into a zcode session. When both are empty the argv is a
// bare interactive TUI launch, matching the convention that an empty prompt
// yields an interactive session.
//
// The task prompt is not passed after a `--` terminator: whether zcode's -p
// honors one is unverified, and the combined prompt is a single argv element
// that is not re-parsed, so the same leading-dash caveat as pi applies to the
// whole -p value.
//
// cfg.SessionID and cfg.NativeSessionID are intentionally unused: zcode has no
// --session-id pin flag. cfg.Config.Model is intentionally not forwarded (see
// the package doc), and LaunchConfig.AllowedTools/DisallowedTools have no
// zcode flag to carry them.
func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) (cmd []string, err error) {
	// Defense-in-depth: the project service validates on write, but re-check
	// here so a config written by any other path can't launch a bad command.
	if err := cfg.Config.Validate(); err != nil {
		return nil, fmt.Errorf("zcode: %w", err)
	}

	binary, err := p.zcodeBinary(ctx)
	if err != nil {
		return nil, err
	}

	cmd = []string{binary}
	if cfg.WorkspacePath != "" {
		cmd = append(cmd, "--cwd", cfg.WorkspacePath)
	}
	// A project's configured permissions drive the starting mode; the explicit
	// LaunchConfig.Permissions wins when set so a per-spawn override still takes
	// precedence over the stored project default.
	appendModeFlags(&cmd, effectivePermissions(cfg.Permissions, cfg.Config))

	effective, err := composePrompt(cfg)
	if err != nil {
		return nil, err
	}
	if effective != "" {
		cmd = append(cmd, "-p", effective)
	}
	return cmd, nil
}

// effectivePermissions resolves the permission mode a launch or restore maps
// onto zcode's --mode: the explicit per-spawn override first, then the
// project's stored config.
func effectivePermissions(explicit ports.PermissionMode, cfg ports.AgentConfig) ports.PermissionMode {
	if explicit != "" {
		return explicit
	}
	return cfg.Permissions
}

// composePrompt resolves the standing instructions and composes the -p value:
// instructions first (they become the native session's first turn), then the
// task prompt after a blank line.
func composePrompt(cfg ports.LaunchConfig) (string, error) {
	instructions, err := resolveSystemPrompt(cfg.SystemPrompt, cfg.SystemPromptFile)
	if err != nil {
		return "", err
	}
	instructions = strings.TrimSpace(instructions)
	switch {
	case instructions == "":
		return cfg.Prompt, nil
	case cfg.Prompt == "":
		return instructions, nil
	default:
		return instructions + "\n\n" + cfg.Prompt, nil
	}
}

// resolveSystemPrompt returns the standing instructions, preferring
// SystemPromptFile (read from disk) over an inline SystemPrompt. A read
// failure aborts the launch.
func resolveSystemPrompt(inline, file string) (string, error) {
	if file != "" {
		data, err := os.ReadFile(file) //nolint:gosec // path is AO-owned launch config
		if err != nil {
			return "", fmt.Errorf("zcode: read system prompt file: %w", err)
		}
		return strings.TrimSpace(string(data)), nil
	}
	return inline, nil
}

// appendModeFlags maps AO's permission modes onto zcode's --mode values.
// `zcode --help` enumerates five modes — build, edit, plan, yolo, guarded —
// with "(default: yolo for --prompt)". The mapping:
//
//   - accept-edits       → --mode edit
//   - auto               → --mode build
//   - bypass-permissions → --mode yolo
//   - default, empty, unrecognized → --mode edit (pinned)
//
// plan and guarded are deliberately unmapped: AO has no vocabulary value for
// them. The default pin is deliberate: emitting no flag would let a -p run
// inherit zcode's documented yolo default (all approvals skipped) whenever a
// project config leaves permissions unset, so every untouched zcode worker
// would run with no approvals. Pinned edit is the same mode an explicit
// accept-edits spawn gets, i.e. AO's own safer baseline.
func appendModeFlags(cmd *[]string, permissions ports.PermissionMode) {
	switch ports.NormalizePermissionMode(permissions) {
	case ports.PermissionModeAcceptEdits:
		*cmd = append(*cmd, "--mode", "edit")
	case ports.PermissionModeAuto:
		*cmd = append(*cmd, "--mode", "build")
	case ports.PermissionModeBypassPermissions:
		*cmd = append(*cmd, "--mode", "yolo")
	case ports.PermissionModeDefault:
		// Pinned safer baseline (see above); never defer to zcode's
		// documented yolo default for --prompt runs.
		*cmd = append(*cmd, "--mode", "edit")
	}
}

// GetPromptDeliveryStrategy, GetAgentHooks, and SessionInfo's empty case come
// from the embedded agentbase.Base: the prompt rides in the launch command
// (one-shot -p), v1 installs no workspace hooks (see the package doc), and a
// metadata-less session reports no info.

// GetRestoreCommand rebuilds the argv that continues an existing ZCode session
// in its worktree. Two shapes, in decision order:
//
//	zcode --cwd <worktree> --mode <mode> --resume <sess_...>   (vestigial, nothing populates it)
//	zcode --cwd <worktree> --mode <mode> -c                    (primary, cwd-scoped continue)
//
// -c ("resume the latest session for the current directory") is deterministic
// because each AO session owns its own worktree. The --resume branch reads
// agentSessionId from session metadata (ports.MetadataKeyAgentSessionID) and
// is kept only as an upgrade path for a future capture probe: no code at HEAD
// writes agentSessionId for zcode (the ao hooks pipeline posts only {state}),
// so it is never taken today — zcode's -c is cwd-scoped and cannot consume the
// metadata id at all.
//
// The --mode flags are re-applied on restore with the same mapping and
// precedence as launch (explicit RestoreConfig.Permissions, then the stored
// project config, pinned edit when neither is set). Whether -c/--resume reset
// the native session's /mode state is unverified, so re-applying the pin is
// the only way a restored session cannot silently fall back onto zcode's
// flagless yolo default.
//
// Two reachability facts, stated deliberately:
//
//   - ok=false only fires when WorkspacePath is empty, and via the manager that
//     is unreachable — Restore pre-rejects workspace-less sessions with
//     ErrIncompleteHandle, and restoreArgv always passes WorkspacePath. So a
//     failed `-c` has NO saved-prompt fresh-relaunch safety net: it surfaces as
//     a pane error and drops to the keep-alive shell. Accepted, because the -c
//     target (latest session for the cwd) is exactly what a prior run left
//     behind.
//   - AO Restore and the operator's own `zcode -c` are two interfering paths,
//     not one story. Restore requires a terminated record, but the keep-alive
//     shell keeps tmux has-session true indefinitely, so a finished -p run
//     stays "running" in AO and cannot be restored until the tmux session is
//     killed — which destroys the very pane an operator may have just
//     continued with `zcode -c`. And an operator-started `zcode` session in
//     the same worktree becomes the new -c restore target.
func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) (cmd []string, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	ws := cfg.Session.WorkspacePath
	if ws == "" {
		// Unreachable via the manager (see doc comment): no workspace, no
		// restore, and no saved-prompt fallback from here.
		return nil, false, nil
	}
	sessionID := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])

	binary, err := p.zcodeBinary(ctx)
	if err != nil {
		return nil, false, err
	}
	cmd = []string{binary, "--cwd", ws}
	appendModeFlags(&cmd, effectivePermissions(cfg.Permissions, cfg.Config))
	if sessionID != "" {
		// Vestigial: nothing at HEAD writes agentSessionId for zcode. Kept
		// first so a future capture probe populates it without touching this
		// file's structure.
		cmd = append(cmd, "--resume", sessionID)
	} else {
		cmd = append(cmd, "-c")
	}
	return cmd, true, nil
}

// SessionInfo surfaces the session metadata AO's store carries for zcode: the
// native session id, title, and summary, passed straight through. It reads
// only from session.Metadata — never from transcript files — and returns
// ok=false when none of those fields are present. Like the --resume branch,
// this is vestigial today: it reads keys nothing writes for zcode at HEAD, so
// it stays ok=false until a capture probe (or the future plugin hooks) starts
// populating them.
func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(session)
	return info, ok, nil
}

// ResolveZcodeBinary finds the `zcode` binary, searching PATH then common
// install locations (see zcodeBinarySpec). On this machine's layout that is
// the sh wrapper at ~/.local/bin/zcode. It returns an error wrapping
// ports.ErrAgentBinaryNotFound when nothing resolves.
func ResolveZcodeBinary(ctx context.Context) (string, error) {
	return binaryutil.ResolveBinary(ctx, zcodeBinarySpec)
}

// InvalidateBinaryResolution drops the cached executable path so the next
// readiness, launch or chat-driver operation resolves the current
// installation again (ports.AgentBinaryResolutionInvalidator).
func (p *Plugin) InvalidateBinaryResolution() {
	p.binaryMu.Lock()
	p.resolvedBinary = ""
	p.binaryMu.Unlock()
}

func (p *Plugin) zcodeBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}

	binary, err := ResolveZcodeBinary(ctx)
	if err != nil {
		return "", err
	}
	p.resolvedBinary = binary
	return binary, nil
}
