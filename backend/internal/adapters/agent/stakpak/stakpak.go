// Package stakpak integrates the official Stakpak Agent interactive TUI
// (https://github.com/stakpak/agent).
//
// Launch shape:
//
//	stakpak --system-prompt-file <file> [--model <model>]
//	stakpak --system-prompt-file <file> [--model <model>] --session <id>
//
// The interactive TUI ignores a positional prompt (only `-p`/`--async` read
// it), so the first task is typed into the native composer after the TUI is
// ready. --system-prompt-file inserts AO's standing instructions as the
// conversation's leading system message, on a fresh launch and on resume.
//
// Stakpak has no external hook surface: its hooks are compiled-in Rust
// traits. Activity therefore comes from the rendered TUI (terminal.go), and
// the native session id from Stakpak's own session list, matched by the
// worktree and launch time (session.go).
//
// Chat is not offered. Stakpak 0.3.88's ACP server answers session/load by
// recording the id only: it neither reloads nor replays the stored messages,
// so a restored Chat would continue without its history. Its ACP model list
// also omits custom providers, and a profile model without a built-in
// provider prefix is routed to the Stakpak API.
package stakpak

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const adapterID = "stakpak"

// Plugin is the Stakpak Agent adapter.
type Plugin struct{ agentbase.Base }

// New returns a ready-to-register Stakpak adapter.
func New() *Plugin { return &Plugin{} }

var (
	_ adapters.Adapter                        = (*Plugin)(nil)
	_ ports.Agent                             = (*Plugin)(nil)
	_ ports.AgentBinaryResolver               = (*Plugin)(nil)
	_ ports.AgentExitDetector                 = (*Plugin)(nil)
	_ ports.AgentNativeSessionResolver        = (*Plugin)(nil)
	_ ports.AgentInterfaceHandoffHistoryProbe = (*Plugin)(nil)
)

// Manifest returns the adapter's static self-description.
func (*Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:           adapterID,
		Name:         "Stakpak",
		Description:  "Official Stakpak Agent interactive terminal sessions with exact native resume.",
		Version:      "0.0.1",
		Capabilities: []adapters.Capability{adapters.CapabilityAgent},
	}
}

// ExitDetectionMode opts Stakpak into AO's process supervisor: with no hook
// surface, nothing else reports that the TUI exited.
func (*Plugin) ExitDetectionMode() ports.AgentExitDetectionMode {
	return ports.AgentExitDetectionSupervisor
}

var binarySpec = binaryutil.BinarySpec{
	Label:    "stakpak",
	Names:    []string{"stakpak"},
	WinNames: []string{"stakpak.exe"},
	UnixPaths: []string{
		"/opt/homebrew/bin/stakpak", "/usr/local/bin/stakpak",
		"/home/linuxbrew/.linuxbrew/bin/stakpak", "/usr/bin/stakpak",
	},
	UnixHomePaths:    [][]string{{".local", "bin", "stakpak"}},
	ValidateIdentity: isStakpakBinary,
}

// isStakpakBinary confirms the executable is Stakpak itself: `stakpak` is a
// generic enough name that the first PATH match is not trusted.
func isStakpakBinary(ctx context.Context, path string) bool {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := process.CommandContext(ctx, path, "--version").Output()
	return err == nil && parseVersion(string(out)) != ""
}

// parseVersion reads `stakpak 0.3.88`, the whole of `stakpak --version`.
func parseVersion(out string) string {
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) != 2 || fields[0] != "stakpak" {
		return ""
	}
	return fields[1]
}

// ResolveBinary finds the official `stakpak` executable.
func (*Plugin) ResolveBinary(ctx context.Context) (string, error) {
	return binaryutil.ResolveBinary(ctx, binarySpec)
}

// GetConfigSpec reports the model override. Stakpak takes provider-prefixed
// names for configured providers (for example "anthropic/claude-sonnet-4-5"
// or a custom provider's "<provider>/<model>").
func (*Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx,
		"Model passed to stakpak --model, for example anthropic/claude-sonnet-4-5 or <custom-provider>/<model> from ~/.stakpak/config.toml. Empty uses the active profile's model.")
}

// GetPromptDeliveryStrategy reports after-start delivery: the interactive TUI
// does not read a positional prompt.
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
	prompt, err := standingInstructions(cfg.SystemPrompt, cfg.SystemPromptFile)
	if err != nil {
		return nil, err
	}
	argv, err := p.baseArgv(ctx, cfg.DataDir, cfg.SessionID, prompt, cfg.SystemPromptFile, cfg.Config)
	if err != nil {
		return nil, err
	}
	if err := writeLaunchRecord(cfg.DataDir, cfg.SessionID, launchRecord{Workspace: cfg.WorkspacePath, LaunchedAt: time.Now().UTC(), PromptDigest: promptDigest(prompt)}); err != nil {
		return nil, err
	}
	return argv, nil
}

// GetRestoreCommand resumes the native conversation by id. Without a known id,
// or when Stakpak no longer has that conversation, ok is false and AO launches
// fresh in the same workspace.
func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if err := validateLaunch(cfg.Config, cfg.AllowedTools, cfg.DisallowedTools); err != nil {
		return nil, false, err
	}
	nativeID := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if nativeID == "" {
		resolved, ok, err := p.ResolveNativeSessionID(ctx, ports.NativeSessionResolveConfig{DataDir: cfg.DataDir, SessionID: domainSessionID(cfg.Session.ID)})
		if err != nil || !ok {
			return nil, false, err
		}
		nativeID = resolved
	}
	if !validSessionID(nativeID) {
		return nil, false, fmt.Errorf("%s: native session id %q is not a Stakpak session UUID", adapterID, nativeID)
	}
	exists, err := p.NativeConversationExists(ctx, cfg.Session, nativeID, nil)
	if err != nil {
		return nil, false, err
	}
	if !exists {
		return nil, false, nil
	}
	prompt, err := standingInstructions(cfg.SystemPrompt, cfg.SystemPromptFile)
	if err != nil {
		return nil, false, err
	}
	// Stakpak stores --system-prompt-file's message in the conversation and
	// inserts it again on every resume. Instructions the conversation already
	// holds are not passed a second time.
	previous, _, err := readLaunchRecord(cfg.DataDir, cfg.Session.ID)
	if err != nil {
		return nil, false, err
	}
	digest := promptDigest(prompt)
	launchPrompt, launchFile := prompt, cfg.SystemPromptFile
	if digest != "" && previous.PromptDigest == digest && previous.matches(nativeID) {
		launchPrompt, launchFile = "", ""
	}
	argv, err := p.baseArgv(ctx, cfg.DataDir, cfg.Session.ID, launchPrompt, launchFile, cfg.Config)
	if err != nil {
		return nil, false, err
	}
	if err := writeLaunchRecord(cfg.DataDir, cfg.Session.ID, launchRecord{Workspace: cfg.Session.WorkspacePath, LaunchedAt: time.Now().UTC(), NativeID: nativeID, PromptDigest: digest}); err != nil {
		return nil, false, err
	}
	return append(argv, "--session", nativeID), true, nil
}

// SessionInfo reports the native session id captured for this launch.
func (p *Plugin) SessionInfo(ctx context.Context, ref ports.SessionRef) (ports.SessionInfo, bool, error) {
	if info, ok := agentbase.StandardSessionInfo(ref); ok {
		return info, true, ctx.Err()
	}
	id, ok, err := p.ResolveNativeSessionID(ctx, ports.NativeSessionResolveConfig{DataDir: ref.DataDir, SessionID: domainSessionID(ref.ID)})
	if err != nil || !ok {
		return ports.SessionInfo{}, false, err
	}
	return ports.SessionInfo{AgentSessionID: id}, true, nil
}

func (p *Plugin) baseArgv(ctx context.Context, dataDir, sessionID, inline, file string, cfg ports.AgentConfig) ([]string, error) {
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return nil, err
	}
	argv := []string{binary}
	promptFile, err := systemPromptFile(dataDir, sessionID, inline, file)
	if err != nil {
		return nil, err
	}
	if promptFile != "" {
		argv = append(argv, "--system-prompt-file", promptFile)
	}
	agentbase.AppendModelFlag(&argv, cfg, "--model")
	return argv, nil
}

// validateLaunch rejects settings Stakpak's TUI has no flag for, instead of
// launching a session that silently ignores them.
func validateLaunch(cfg ports.AgentConfig, allowed, disallowed []string) error {
	if strings.TrimSpace(cfg.Effort) != "" {
		return errors.New("stakpak: reasoning effort overrides are not supported by the Stakpak TUI")
	}
	if len(allowed) > 0 || len(disallowed) > 0 {
		return errors.New("stakpak: per-launch tool restrictions are not supported")
	}
	return nil
}

// standingInstructions resolves AO's standing instructions: inline wins, else
// the file is read. Empty means none.
func standingInstructions(inline, file string) (string, error) {
	if strings.TrimSpace(inline) != "" {
		return inline, nil
	}
	if strings.TrimSpace(file) == "" {
		return "", nil
	}
	data, err := os.ReadFile(file) //nolint:gosec // AO-owned launch config path
	if err != nil {
		return "", fmt.Errorf("stakpak: read system prompt file: %w", err)
	}
	return string(data), nil
}

// promptDigest fingerprints standing instructions; empty for none.
func promptDigest(prompt string) string {
	if strings.TrimSpace(prompt) == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(prompt)))
	return hex.EncodeToString(sum[:])
}

// systemPromptFile returns the file passed to --system-prompt-file: AO's own
// file when it wrote one, else the instructions written under the AO data
// dir. Empty means no standing instructions.
func systemPromptFile(dataDir, sessionID, inline, file string) (string, error) {
	if strings.TrimSpace(inline) == "" {
		return "", nil
	}
	if strings.TrimSpace(file) != "" {
		return file, nil
	}
	if strings.TrimSpace(dataDir) == "" || strings.TrimSpace(sessionID) == "" {
		return "", errors.New("stakpak: a system prompt needs the AO data dir and session id")
	}
	dir := sessionDir(dataDir, sessionID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("stakpak: create session dir: %w", err)
	}
	path := filepath.Join(dir, "system-prompt.md")
	if err := os.WriteFile(path, []byte(inline), 0o600); err != nil {
		return "", fmt.Errorf("stakpak: write system prompt: %w", err)
	}
	return path, nil
}

// sessionDir holds AO-owned per-session Stakpak state under the AO data dir.
func sessionDir(dataDir, sessionID string) string {
	return filepath.Join(dataDir, "agents", adapterID, sessionID)
}
