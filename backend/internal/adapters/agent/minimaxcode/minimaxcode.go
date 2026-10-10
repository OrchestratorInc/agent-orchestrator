// Package minimaxcode adapts the source-correlated MiniMax Code TUI release.
// MiniMax Code is distinct from the Xiaomi MiMo Code harness.
package minimaxcode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const adapterID = "minimax-code"

var aoIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
var nativeIDPattern = regexp.MustCompile(`^mvs_[a-f0-9]{32}$`)

// Plugin implements MiniMax Code's terminal-only worker contract.
type Plugin struct {
	agentbase.Base
	binaryMu       sync.Mutex
	resolvedBinary string
}

// New constructs the MiniMax Code TUI adapter.
func New() *Plugin { return &Plugin{} }

var _ ports.Agent = (*Plugin)(nil)
var _ ports.AgentRuntimeLaunchEnv = (*Plugin)(nil)
var _ ports.AgentAuthChecker = (*Plugin)(nil)
var _ ports.ContinuousTerminalActivityDetector = (*Plugin)(nil)
var _ ports.EmptyComposerDetector = (*Plugin)(nil)

// Manifest identifies the terminal-only MiniMax harness.
func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{ID: adapterID, Name: "MiniMax Code", Description: "MiniMax Code terminal worker sessions.", Version: "0.0.1", Capabilities: []adapters.Capability{adapters.CapabilityAgent}}
}

// GetConfigSpec exposes the configured provider/model selector.
func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx, "Configured MiniMax provider/model ID for this session.")
}

// GetLaunchCommand prepares a fresh native conversation with an identity guard.
func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	if err := validateLaunch(ctx, cfg.Permissions, cfg.SystemPrompt, cfg.SystemPromptFile); err != nil {
		return nil, err
	}
	profile, err := profilePath(cfg.DataDir, cfg.SessionID)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(profile, 0o700); err != nil {
		return nil, err
	}
	if err := hookutil.AtomicWriteFile(filepath.Join(profile, "ao-native-id"), []byte("\n"), 0o600); err != nil {
		return nil, err
	}
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return nil, err
	}
	return launchArgs(binary, cfg.Config.Model, cfg.SystemPromptFile, "", cfg.Prompt), nil
}

// GetRestoreCommand validates and restores the exact recorded conversation.
func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	if err := validateLaunch(ctx, cfg.Permissions, cfg.SystemPrompt, cfg.SystemPromptFile); err != nil {
		return nil, false, err
	}
	nativeID := cfg.Session.Metadata[ports.MetadataKeyAgentSessionID]
	if !nativeIDPattern.MatchString(nativeID) {
		return nil, false, errors.New("MiniMax restore requires an exact native mvs_ session ID")
	}
	profile, err := profilePath(cfg.DataDir, cfg.Session.ID)
	if err != nil {
		return nil, false, err
	}
	if err := validateNativeHistory(ctx, profile, nativeID, cfg.Session.WorkspacePath); err != nil {
		return nil, false, fmt.Errorf("MiniMax restore refused: %w", err)
	}
	if err := hookutil.AtomicWriteFile(filepath.Join(profile, "ao-native-id"), []byte(nativeID+"\n"), 0o600); err != nil {
		return nil, false, err
	}
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return nil, false, err
	}
	return launchArgs(binary, cfg.Config.Model, cfg.SystemPromptFile, nativeID, cfg.Prompt), true, nil
}
func validateLaunch(ctx context.Context, mode ports.PermissionMode, prompt, file string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch mode {
	case "", ports.PermissionModeDefault, ports.PermissionModeAuto, ports.PermissionModeBypassPermissions:
	default:
		return errors.New("MiniMax TUI supports only default, auto, and bypassPermissions")
	}
	if prompt != "" && file == "" {
		return errors.New("MiniMax standing instructions require an AO-owned prompt file")
	}
	return nil
}
func launchArgs(binary, model, promptFile, nativeID, prompt string) []string {
	args := []string{binary}
	if strings.TrimSpace(model) != "" {
		args = append(args, "--model", strings.TrimSpace(model))
	}
	if promptFile != "" {
		args = append(args, "--append-system-prompt-file", promptFile)
	}
	if nativeID != "" {
		args = append(args, "--session", nativeID)
	}
	if prompt != "" {
		args = append(args, "--", prompt)
	}
	return args
}
func profilePath(dataDir, sessionID string) (string, error) {
	if !filepath.IsAbs(dataDir) || !aoIDPattern.MatchString(sessionID) {
		return "", errors.New("MiniMax requires an absolute AO data directory and valid session ID")
	}
	return filepath.Join(dataDir, "agents", adapterID, sessionID), nil
}

// AugmentRuntimeLaunchEnv selects the private profile and immutable launch directory.
func (p *Plugin) AugmentRuntimeLaunchEnv(env map[string]string, dataDir string, id domain.SessionID, launchID string) {
	profile, err := profilePath(dataDir, string(id))
	if err != nil {
		return
	}
	env["MINIMAX_DATA_DIR"] = profile
	env["MAVIS_DATA_DIR"] = profile
	tmp := filepath.Join(profile, "launches", launchID, "tmp")
	env["TMPDIR"] = tmp
	env["TEMP"] = tmp
	env["TMP"] = tmp
}

// SessionInfo returns the persisted native session metadata.
func (p *Plugin) SessionInfo(ctx context.Context, s ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(s)
	return info, ok, nil
}

// EmitsSemanticMessageAcceptance reports the native prompt-submission hook.
func (p *Plugin) EmitsSemanticMessageAcceptance() bool { return true }

// EmitsSubmitActivity reports activity when MiniMax accepts a prompt.
func (p *Plugin) EmitsSubmitActivity() bool { return true }

// EmitsBlockedActivity reports native permission requests.
func (p *Plugin) EmitsBlockedActivity() bool { return true }

var binarySpec = binaryutil.BinarySpec{Label: "mcode", Names: []string{"mcode"}, WinNames: []string{"mcode.cmd", "mcode.exe", "mcode"}, UnixPaths: []string{"/usr/local/bin/mcode", "/opt/homebrew/bin/mcode"}, UnixHomePaths: binaryutil.NodeManagedUnixHomePaths("mcode"), NodeManaged: true, WinPaths: []binaryutil.WinPath{{Base: binaryutil.WinAppData, Parts: []string{"npm", "mcode.cmd"}}}}

// ResolveBinary requires the source-correlated MiniMax release.
func (p *Plugin) ResolveBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()
	if p.resolvedBinary != "" {
		return p.resolvedBinary, ctx.Err()
	}
	b, err := binaryutil.ResolveBinary(ctx, binarySpec)
	if err == nil {
		probe, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		version, versionErr := aoprocess.CommandContext(probe, b, "--version").Output()
		if versionErr != nil || strings.TrimSpace(string(version)) != "0.6.5" {
			return "", errors.New("MiniMax Code TUI requires source-correlated version 0.6.5")
		}
		p.resolvedBinary = b
	}
	return b, err
}

// InvalidateBinaryResolution clears the cached executable after installation changes.
func (p *Plugin) InvalidateBinaryResolution() {
	p.binaryMu.Lock()
	p.resolvedBinary = ""
	p.binaryMu.Unlock()
}
