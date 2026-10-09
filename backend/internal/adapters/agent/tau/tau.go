// Package tau integrates the released Tau Textual TUI, not its headless RPC mode.
package tau

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

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// SupportedVersion is the released contract inspected for this adapter.
const SupportedVersion = "0.4.7"
const managedSentinel = "agent-orchestrator: managed tau integration"

var nativeIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
var versionPattern = regexp.MustCompile(`(?m)^tau ([0-9]+\.[0-9]+\.[0-9]+)\s*$`)

type commandRunner func(context.Context, string, ...string) ([]byte, error)

// Plugin preserves the user's provider settings and native session store.
type Plugin struct {
	agentbase.Base
	binaryMu       sync.Mutex
	resolvedBinary string
	run            commandRunner
}

// New constructs the Tau adapter.
func New() *Plugin { return &Plugin{} }

var _ ports.Agent = (*Plugin)(nil)
var _ ports.AgentBinaryResolver = (*Plugin)(nil)
var _ ports.StartupInputReadinessSignaler = (*Plugin)(nil)
var _ ports.AgentInterruptInputProvider = (*Plugin)(nil)

// Manifest describes the supported terminal contract.
func (*Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{ID: "tau", Name: "Tau", Description: "Tau terminal sessions; requires explicit bypass permissions (Tau has no tool approval policy).", Version: SupportedVersion, Capabilities: []adapters.Capability{adapters.CapabilityAgent}}
}

// GetConfigSpec exposes configured provider/model selection.
func (*Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx, "Configured Tau provider/model. Tau requires explicit bypass permissions; project trust is not a tool approval policy.")
}

// GetPromptDeliveryStrategy keeps tasks out of Tau command dispatch.
func (*Plugin) GetPromptDeliveryStrategy(ctx context.Context, _ ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	return ports.PromptDeliveryAfterStart, ctx.Err()
}

// FirstSignalProvesInputReady is true because Tau emits session_start from on_mount after focusing the native composer.
func (*Plugin) FirstSignalProvesInputReady() bool { return true }

// ExitDetectionMode requires process supervision for abrupt shutdowns.
func (*Plugin) ExitDetectionMode() ports.AgentExitDetectionMode {
	return ports.AgentExitDetectionSupervisor
}

// InterruptInput cancels Tau turns with Escape.
func (*Plugin) InterruptInput() string { return "\x1b" }

// PromptReadinessHints requires the mounted native composer before typing.
func (*Plugin) PromptReadinessHints(ctx context.Context, _ ports.LaunchConfig) (ports.PromptReadinessHints, error) {
	return ports.PromptReadinessHints{RequireReady: true, Patterns: []string{"Ask Tau…"}, PollInterval: 100 * time.Millisecond, Timeout: 20 * time.Second, Lines: 80}, ctx.Err()
}

// GetLaunchCommand starts a new foreground native TUI session.
func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	cmd, err := p.command(ctx, cfg.WorkspacePath, cfg.Config, cfg.Permissions, cfg.SystemPrompt, cfg.SystemPromptFile)
	if err != nil {
		return nil, err
	}
	// Positional tasks such as "sessions" and "--version" are CLI commands.
	// Deliver through the mounted terminal instead of passing any task in argv.
	return append(cmd, "--new-session"), nil
}

// GetRestoreCommand resumes only an exact persisted native identity.
func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	id := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if id == "" {
		return nil, false, nil
	}
	// TUI-created sessions use uuid4().hex. Never accept prefixes, paths or aliases.
	if !nativeIDPattern.MatchString(id) {
		return nil, false, fmt.Errorf("tau: invalid native session ID")
	}
	cmd, err := p.command(ctx, cfg.Session.WorkspacePath, cfg.Config, cfg.Permissions, cfg.SystemPrompt, cfg.SystemPromptFile)
	if err != nil {
		return nil, false, err
	}
	return append(cmd, "--session", id), true, nil
}

func (p *Plugin) command(ctx context.Context, workspace string, cfg ports.AgentConfig, permissions ports.PermissionMode, prompt, promptFile string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if permissions != ports.PermissionModeBypassPermissions {
		return nil, fmt.Errorf("tau: select bypass-permissions explicitly; Tau does not implement manual, accept-edits or auto tool approvals")
	}
	if strings.TrimSpace(workspace) == "" {
		return nil, fmt.Errorf("tau: workspace is required")
	}
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return nil, err
	}
	out, err := p.probe(ctx, binary, "--version")
	if err != nil {
		return nil, fmt.Errorf("tau: version probe: %w", err)
	}
	match := versionPattern.FindStringSubmatch(string(out))
	if len(match) != 2 || match[1] != SupportedVersion {
		return nil, fmt.Errorf("tau: this adapter requires tau-ai %s; update AO before changing the Tau contract", SupportedVersion)
	}
	cmd := []string{binary, "--cwd", workspace, "--approve", "--no-extensions", "--extension", extensionPath(workspace)}
	// --approve admits project AGENTS.md for this process; it is not a tool grant.
	if prompt != "" {
		promptFile = filepath.Join(workspace, ".tau", "ao-standing-instructions.md")
		if err := writeManaged(promptFile, "<!-- "+managedSentinel+" -->\n"+prompt); err != nil {
			return nil, err
		}
	}
	if promptFile != "" {
		info, err := os.Stat(promptFile)
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("tau: standing instruction file is unavailable")
		}
		cmd = append(cmd, "--append-system-prompt", promptFile)
	}
	if model := strings.TrimSpace(cfg.Model); model != "" {
		if provider, modelID, ok := strings.Cut(model, "/"); ok {
			if provider == "" || modelID == "" {
				return nil, fmt.Errorf("tau: model must be provider/model")
			}
			cmd = append(cmd, "--provider", provider, "--model", modelID)
		} else {
			cmd = append(cmd, "--model", model)
		}
	}
	return cmd, nil
}

// SessionInfo reports AO-captured native identity and metadata.
func (*Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(session)
	return info, ok, nil
}

// ResolveBinary finds the installed Tau entry point without invoking it.
func (p *Plugin) ResolveBinary(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()
	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}
	path, err := binaryutil.ResolveBinary(ctx, binaryutil.BinarySpec{Label: "tau", Names: []string{"tau"}, WinNames: []string{"tau.exe", "tau.cmd", "tau"}, UnixPaths: []string{"/usr/local/bin/tau", "/opt/homebrew/bin/tau"}, UnixHomePaths: [][]string{{".local", "bin", "tau"}}, WinPaths: []binaryutil.WinPath{{Base: binaryutil.WinHome, Parts: []string{".local", "bin", "tau.exe"}}}})
	if err == nil {
		p.resolvedBinary = path
	}
	return path, err
}

func (p *Plugin) probe(ctx context.Context, binary string, args ...string) ([]byte, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if p.run != nil {
		return p.run(probeCtx, binary, args...)
	}
	cmd := aoprocess.CommandContext(probeCtx, binary, args...)
	cmd.WaitDelay = 2 * time.Second
	return cmd.Output()
}

func writeManaged(path, content string) error {
	if old, err := os.ReadFile(path); err == nil { //nolint:gosec // caller-owned workspace path
		if !strings.Contains(string(old), managedSentinel) {
			return fmt.Errorf("tau: refusing to overwrite non-AO file %s", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return err
	}
	if err := hookutil.AtomicWriteFile(path, []byte(content), 0600); err != nil {
		return err
	}
	return hookutil.EnsureWorkspaceGitignore(filepath.Dir(path), filepath.Base(path))
}
