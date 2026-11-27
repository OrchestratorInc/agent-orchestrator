// Package pax integrates Pax (https://pypi.org/project/pydantic-ai-pax/), the
// Pydantic AI terminal coding agent, as an interactive TUI harness.
//
// Launch shape:
//
//	pax --session-id <uuid> [-m <provider:model>]
//	pax --resume <uuid> [-m <provider:model>]
//
// Pax's interactive shell reads no positional prompt (`-p` runs one headless
// turn and exits), so the first task is typed into the native `❯` editor once
// it is drawn (terminal.go).
//
// AO chooses the native conversation id with --session-id, so restore is by
// exact id. Pax saves a conversation only after its first turn, so restore
// first checks Pax's own sessions.db, read-only (store.go).
//
// Pax has no system-prompt flag. AO's standing instructions, and a permission
// mode stricter than the user's own, go in an AO-owned, git-ignored
// `.clai/settings.json` in the worktree (project.go). Pax sends unapproved
// project instructions to the model as the repository's text.
//
// Pax refuses to install plugins or hooks from a shell (the agent can run
// `pax` too), so AO cannot install hooks for a session. Activity comes from
// the rendered TUI; a user who adds AO's hook table once (docs/harnesses/pax.md)
// also reports turns through `ao hooks pax` (hooks.go).
//
// Chat is not offered: Pax has no ACP server.
package pax

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const adapterID = "pax"

// sessionNamespace derives a stable native conversation id from an AO session
// id, so a fresh launch needs no stored state to be restored.
var sessionNamespace = uuid.MustParse("6f2b8c1e-4a7d-5e93-9b20-3c5d7e8f1a64")

// Plugin is the Pax adapter.
type Plugin struct {
	agentbase.Base
	binaryMu       sync.Mutex
	resolvedBinary string
	// recordMu serializes launch-record writes.
	recordMu sync.Mutex
}

// New returns a ready-to-register Pax adapter.
func New() *Plugin { return &Plugin{} }

var (
	_ adapters.Adapter                        = (*Plugin)(nil)
	_ ports.Agent                             = (*Plugin)(nil)
	_ ports.AgentBinaryResolver               = (*Plugin)(nil)
	_ ports.AgentBinaryResolutionInvalidator  = (*Plugin)(nil)
	_ ports.AgentExitDetector                 = (*Plugin)(nil)
	_ ports.AgentInterruptInputProvider       = (*Plugin)(nil)
	_ ports.AgentInterfaceHandoffHistoryProbe = (*Plugin)(nil)
)

// Manifest returns the adapter's static self-description.
func (*Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:           adapterID,
		Name:         "Pax",
		Description:  "Pax (Pydantic AI) interactive terminal sessions with exact native resume.",
		Version:      "0.0.1",
		Capabilities: []adapters.Capability{adapters.CapabilityAgent},
	}
}

// ExitDetectionMode opts Pax into AO's process supervisor: AO installs no
// hook that reports the TUI exiting.
func (*Plugin) ExitDetectionMode() ports.AgentExitDetectionMode {
	return ports.AgentExitDetectionSupervisor
}

// InterruptInput cancels a running turn with Escape. Ctrl+C also cancels, but
// a second Ctrl+C quits Pax; Escape never counts toward that.
func (*Plugin) InterruptInput() string { return "\x1b" }

// binarySpec prefers `pydantic-ai-pax`, the distribution's unambiguous entry
// point: macOS ships an unrelated `/bin/pax` archiver. uv installs both under
// ~/.local/bin on every platform.
var binarySpec = binaryutil.BinarySpec{
	Label:    adapterID,
	Names:    []string{"pydantic-ai-pax", "pax"},
	WinNames: []string{"pydantic-ai-pax.exe", "pax.exe"},
	UnixHomePaths: [][]string{
		{".local", "bin", "pydantic-ai-pax"},
		{".local", "bin", "pax"},
	},
	WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinHome, Parts: []string{".local", "bin", "pydantic-ai-pax.exe"}},
		{Base: binaryutil.WinHome, Parts: []string{".local", "bin", "pax.exe"}},
	},
	ValidateIdentity: isPaxBinary,
}

// isPaxBinary confirms the executable is Pax: `pax --version` prints
// `pax <version>`, while the POSIX archiver rejects the flag.
func isPaxBinary(ctx context.Context, path string) bool {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := process.CommandContext(ctx, path, "--version").Output()
	return err == nil && parseVersion(string(out)) != ""
}

// parseVersion reads `pax 0.75.0`, `pax 0.75.1.dev3+abc (main abc1234)`, or
// `clai2 …` from the deprecated `pydantic-clai2` install of the same package.
func parseVersion(out string) string {
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) < 2 || (fields[0] != "pax" && fields[0] != "clai2") {
		return ""
	}
	version := fields[1]
	if version == "" || version[0] < '0' || version[0] > '9' || !strings.Contains(version, ".") {
		return ""
	}
	return version
}

// ResolveBinary finds the Pax executable. The path is cached because the
// identity check starts Python.
func (p *Plugin) ResolveBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()
	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}
	binary, err := binaryutil.ResolveBinary(ctx, binarySpec)
	if err != nil {
		return "", err
	}
	p.resolvedBinary = binary
	return binary, nil
}

// InvalidateBinaryResolution forgets the cached executable, for example after
// AO installs or updates Pax.
func (p *Plugin) InvalidateBinaryResolution() {
	p.binaryMu.Lock()
	p.resolvedBinary = ""
	p.binaryMu.Unlock()
}

// GetConfigSpec reports the model override.
func (*Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx,
		"Provider-qualified model passed to pax -m, for example anthropic:claude-opus-5-5 or openai-codex:gpt-6-astra. Empty uses your saved Pax model.")
}

// GetPromptDeliveryStrategy reports after-start delivery: the interactive
// shell reads no positional prompt.
func (*Plugin) GetPromptDeliveryStrategy(ctx context.Context, _ ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	return ports.PromptDeliveryAfterStart, ctx.Err()
}

// GetLaunchCommand builds the argv for a fresh interactive session.
func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateLaunch(cfg.Config, cfg.AllowedTools, cfg.DisallowedTools); err != nil {
		return nil, err
	}
	instructions, err := standingInstructions(cfg.SystemPrompt, cfg.SystemPromptFile)
	if err != nil {
		return nil, err
	}
	nativeID, err := p.freshNativeID(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := writeProjectSettings(cfg.WorkspacePath, instructions, cfg.Permissions); err != nil {
		return nil, err
	}
	argv, err := p.baseArgv(ctx, cfg.Config)
	if err != nil {
		return nil, err
	}
	if err := p.saveLaunchRecord(cfg.DataDir, cfg.SessionID, launchRecord{NativeID: nativeID, Workspace: cfg.WorkspacePath, LaunchedAt: time.Now().UTC()}); err != nil {
		return nil, err
	}
	return append(argv, "--session-id", nativeID), nil
}

// freshNativeID returns the conversation id for a fresh launch: the caller's,
// else one derived from the AO session id. A derived id Pax already holds (a
// fresh relaunch after the first conversation was saved) is replaced by a
// random one, since --session-id must name a new conversation.
func (p *Plugin) freshNativeID(ctx context.Context, cfg ports.LaunchConfig) (string, error) {
	if id := strings.TrimSpace(cfg.NativeSessionID); id != "" {
		if !validSessionID(id) {
			return "", fmt.Errorf("%s: native session id %q is not a UUID", adapterID, id)
		}
		return id, nil
	}
	if strings.TrimSpace(cfg.SessionID) == "" {
		return uuid.NewString(), nil
	}
	derived := SessionUUID(cfg.SessionID)
	taken, err := p.NativeConversationExists(ctx, ports.SessionRef{}, derived, cfg.Env)
	if err != nil {
		return "", err
	}
	if taken {
		return uuid.NewString(), nil
	}
	return derived, nil
}

// SessionUUID maps an AO session id onto the Pax conversation id its first
// fresh launch uses.
func SessionUUID(aoSessionID string) string {
	return uuid.NewSHA1(sessionNamespace, []byte(aoSessionID)).String()
}

// GetRestoreCommand resumes the native conversation by id. Without a known id,
// or when Pax has not saved that conversation in this workspace (it saves one
// only after its first turn), ok is false and AO launches fresh.
func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if err := validateLaunch(cfg.Config, cfg.AllowedTools, cfg.DisallowedTools); err != nil {
		return nil, false, err
	}
	nativeID := p.knownNativeID(cfg.DataDir, cfg.Session)
	if nativeID == "" {
		return nil, false, nil
	}
	if !validSessionID(nativeID) {
		return nil, false, fmt.Errorf("%s: native session id %q is not a UUID", adapterID, nativeID)
	}
	saved, err := p.savedConversation(ctx, nativeID, cfg.Env)
	if err != nil {
		return nil, false, err
	}
	if !saved.found {
		return nil, false, nil
	}
	if !sameWorkspace(saved.workspace, cfg.Session.WorkspacePath) {
		// Pax refuses to resume a conversation saved in another folder.
		return nil, false, nil
	}
	instructions, err := standingInstructions(cfg.SystemPrompt, cfg.SystemPromptFile)
	if err != nil {
		return nil, false, err
	}
	if err := writeProjectSettings(cfg.Session.WorkspacePath, instructions, cfg.Permissions); err != nil {
		return nil, false, err
	}
	argv, err := p.baseArgv(ctx, cfg.Config)
	if err != nil {
		return nil, false, err
	}
	if err := p.saveLaunchRecord(cfg.DataDir, cfg.Session.ID, launchRecord{NativeID: nativeID, Workspace: cfg.Session.WorkspacePath, LaunchedAt: time.Now().UTC()}); err != nil {
		return nil, false, err
	}
	return append(argv, "--resume", nativeID), true, nil
}

// knownNativeID is the conversation id AO recorded for the session: hook
// metadata first, else this adapter's launch record.
func (p *Plugin) knownNativeID(dataDir string, session ports.SessionRef) string {
	if id := strings.TrimSpace(session.Metadata[ports.MetadataKeyAgentSessionID]); id != "" {
		return id
	}
	if dataDir == "" {
		dataDir = session.DataDir
	}
	record, ok, err := readLaunchRecord(dataDir, session.ID)
	if err != nil || !ok {
		return ""
	}
	return record.NativeID
}

// SessionInfo reports the native conversation id of the session's latest
// launch.
func (p *Plugin) SessionInfo(ctx context.Context, ref ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	if info, ok := agentbase.StandardSessionInfo(ref); ok {
		return info, true, nil
	}
	record, ok, err := readLaunchRecord(ref.DataDir, ref.ID)
	if err != nil || !ok || record.NativeID == "" {
		return ports.SessionInfo{}, false, err
	}
	return ports.SessionInfo{AgentSessionID: record.NativeID}, true, nil
}

func (p *Plugin) baseArgv(ctx context.Context, cfg ports.AgentConfig) ([]string, error) {
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return nil, err
	}
	argv := []string{binary}
	agentbase.AppendModelFlag(&argv, cfg, "-m")
	return argv, nil
}

// validateLaunch rejects settings Pax's shell has no flag for, instead of
// launching a session that silently ignores them.
func validateLaunch(cfg ports.AgentConfig, allowed, disallowed []string) error {
	if strings.TrimSpace(cfg.Effort) != "" {
		return errors.New("pax: reasoning effort overrides are not supported at launch; use /effort inside Pax")
	}
	if len(allowed) > 0 || len(disallowed) > 0 {
		return errors.New("pax: per-launch tool restrictions are not supported")
	}
	if model := strings.TrimSpace(cfg.Model); model != "" && strings.HasPrefix(model, "-") {
		return fmt.Errorf("pax: model %q is not a provider-qualified model name", model)
	}
	return nil
}

func validSessionID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == strings.ToLower(id)
}
