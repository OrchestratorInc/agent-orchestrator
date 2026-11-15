package cursor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/termtheme"
	"github.com/aoagents/agent-orchestrator/backend/pkg/agentruntime"
)

type Plugin struct {
	agentbase.Base
	binaryMu       sync.Mutex
	resolvedBinary string
}

func New() *Plugin {
	return &Plugin{}
}

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)
var _ ports.StartupInputReadinessSignaler = (*Plugin)(nil)

func (p *Plugin) FirstSignalProvesInputReady() bool { return true }

func cursorDataDir(dataDir string) string {
	return filepath.Join(dataDir, "cursor")
}

func (p *Plugin) AugmentRuntimeEnv(env map[string]string, dataDir string) {
	if strings.TrimSpace(dataDir) == "" {
		return
	}
	env[cursorDataDirEnv] = cursorDataDir(dataDir)
	termtheme.Apply(env, dataDir)
}

func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          "cursor",
		Name:        "Cursor",
		Description: "Run Cursor CLI agent worker sessions.",
		Version:     "0.0.1",
		Capabilities: []adapters.Capability{
			adapters.CapabilityAgent,
		},
	}
}

func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx, "Model override passed to `cursor-agent --model`.")
}

func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) (cmd []string, err error) {
	binary, err := p.cursorBinary(ctx)
	if err != nil {
		return nil, err
	}

	return agentruntime.BuildLaunchCommand(agentruntime.LaunchConfig{
		Harness:    agentruntime.HarnessCursor,
		Binary:     binary,
		Model:      cfg.Config.Model,
		Prompt:     cfg.Prompt,
		Permission: agentruntime.PermissionPolicy(cfg.Permissions),
	})
}

func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) (cmd []string, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if _, ok := agentruntime.RestoreIdentity(
		agentruntime.HarnessCursor,
		cfg.Session.ID,
		cfg.Session.Metadata,
	); !ok {
		return nil, false, nil
	}
	binary, err := p.cursorBinary(ctx)
	if err != nil {
		return nil, false, err
	}

	return agentruntime.BuildRestoreCommand(agentruntime.RestoreConfig{
		Harness:    agentruntime.HarnessCursor,
		Binary:     binary,
		SessionID:  cfg.Session.ID,
		Metadata:   cfg.Session.Metadata,
		Model:      cfg.Config.Model,
		Permission: agentruntime.PermissionPolicy(cfg.Permissions),
	})
}

func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(session)
	return info, ok, nil
}

func ResolveCursorBinary(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	if runtime.GOOS == "windows" {
		for _, name := range []string{"cursor-agent.exe", "cursor-agent.cmd", "cursor-agent"} {
			path, err := exec.LookPath(name)
			if err == nil && path != "" {
				return path, nil
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		for _, candidate := range binaryutil.WindowsPackageManagerBinCandidates("cursor-agent") {
			if hookutil.IsExecutableFile(candidate) {
				return candidate, nil
			}
			if err := ctx.Err(); err != nil {
				return "", err
			}
		}
		return "", fmt.Errorf("cursor: %w", ports.ErrAgentBinaryNotFound)
	}

	if path, err := exec.LookPath("cursor-agent"); err == nil && path != "" {
		return path, nil
	}

	candidates := []string{}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".local", "bin", "cursor-agent"))
		candidates = append(candidates, binaryutil.UnixPackageManagerBinCandidates(home, "cursor-agent")...)
	}
	candidates = append(candidates,
		"/usr/local/bin/cursor-agent",
		"/opt/homebrew/bin/cursor-agent",
	)

	for _, candidate := range candidates {
		if hookutil.IsExecutableFile(candidate) {
			return candidate, nil
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
	}

	return "", fmt.Errorf("cursor: %w", ports.ErrAgentBinaryNotFound)
}

func (p *Plugin) cursorBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}

	binary, err := ResolveCursorBinary(ctx)
	if err != nil {
		return "", err
	}
	p.resolvedBinary = binary
	return binary, nil
}

func (p *Plugin) InvalidateBinaryResolution() {
	p.binaryMu.Lock()
	p.resolvedBinary = ""
	p.binaryMu.Unlock()
}

func (*Plugin) SupportsTerminalVersionRefresh() bool { return true }

var _ ports.AgentTerminalVersionRefresher = (*Plugin)(nil)
