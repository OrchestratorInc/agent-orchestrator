package agy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"

	_ "modernc.org/sqlite"
)

const adapterID = "agy"

var agyBinarySpec = binaryutil.BinarySpec{
	Label:         "agy",
	Names:         []string{"agy"},
	WinNames:      []string{"agy.cmd", "agy.exe", "agy"},
	UnixPaths:     []string{"/usr/local/bin/agy", "/opt/homebrew/bin/agy"},
	UnixHomePaths: binaryutil.NodeManagedUnixHomePaths("agy", []string{".cargo", "bin", "agy"}),
	NodeManaged:   true,
	WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "agy.cmd"}},
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "agy.exe"}},
		{Base: binaryutil.WinHome, Parts: []string{".cargo", "bin", "agy.exe"}},
	},
}

type Plugin struct {
	agentbase.Base
	binaryMu       sync.RWMutex
	resolvedBinary string
}

func New() *Plugin {
	return &Plugin{}
}

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)
var _ ports.AgentInterfaceHandoff = (*Plugin)(nil)
var _ ports.AgentInterfaceHandoffHistoryProbe = (*Plugin)(nil)
var _ ports.AgentInterfaceHandoffStorageBridge = (*Plugin)(nil)
var _ ports.TerminalSurfaceInspector = (*Plugin)(nil)
var _ ports.SubmitActivitySignaler = (*Plugin)(nil)
var _ ports.BlockedActivitySignaler = (*Plugin)(nil)

func (p *Plugin) EmitsSubmitActivity() bool { return true }

func (p *Plugin) EmitsBlockedActivity() bool { return false }

func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          adapterID,
		Name:        "Agy",
		Description: "Run Agy (Antigravity) worker sessions.",
		Version:     "0.0.1",
		Capabilities: []adapters.Capability{
			adapters.CapabilityAgent,
		},
	}
}

func (p *Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	if err := ctx.Err(); err != nil {
		return ports.ConfigSpec{}, err
	}
	return ports.ConfigSpec{
		Fields: []ports.ConfigField{
			{
				Key:         "model",
				Type:        ports.ConfigFieldString,
				Description: "Model override passed to `agy --model` (e.g. gemini-3-pro).",
			},
		},
	}, nil
}

func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) (cmd []string, err error) {
	binary, err := p.agyBinary(ctx)
	if err != nil {
		return nil, err
	}

	cmd = []string{binary}

	if cfg.WorkspacePath != "" {
		cmd = append(cmd, "--add-dir", cfg.WorkspacePath)
	}

	switch ports.NormalizePermissionMode(cfg.Permissions) {
	case ports.PermissionModeAcceptEdits:
		cmd = append(cmd, "--mode", "accept-edits")
	case ports.PermissionModeAuto:
		cmd = append(cmd, "--mode", "auto")
	case ports.PermissionModeBypassPermissions:
		cmd = append(cmd, "--dangerously-skip-permissions")
	default:
	}

	appendModelFlag(&cmd, cfg.Config)

	if cfg.Prompt != "" {
		cmd = append(cmd, "--prompt-interactive", cfg.Prompt)
	}

	return cmd, nil
}

func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) (cmd []string, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}

	agentSessionID := strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if agentSessionID == "" {
		return nil, false, nil
	}

	binary, err := p.agyBinary(ctx)
	if err != nil {
		return nil, false, err
	}

	cmd = []string{binary}

	if cfg.Session.WorkspacePath != "" {
		cmd = append(cmd, "--add-dir", cfg.Session.WorkspacePath)
	}

	switch ports.NormalizePermissionMode(cfg.Permissions) {
	case ports.PermissionModeAcceptEdits:
		cmd = append(cmd, "--mode", "accept-edits")
	case ports.PermissionModeAuto:
		cmd = append(cmd, "--mode", "auto")
	case ports.PermissionModeBypassPermissions:
		cmd = append(cmd, "--dangerously-skip-permissions")
	default:
	}

	appendModelFlag(&cmd, cfg.Config)

	cmd = append(cmd, "--conversation", agentSessionID)
	return cmd, true, nil
}

func (p *Plugin) NativeConversationExists(
	ctx context.Context,
	session ports.SessionRef,
	nativeConversationID string,
	env map[string]string,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	id := strings.TrimSpace(nativeConversationID)
	if !validConversationID(id) {
		return false, nil
	}
	home, err := GeminiHome(env)
	if err != nil {
		return false, err
	}
	modeDirs := []string{ACPStorageDir, CLIStorageDir}
	if rawMode := session.Metadata[ports.MetadataKeySessionMode]; rawMode != "" {
		if mode := domain.NormalizeSessionMode(domain.SessionMode(rawMode)); mode == domain.SessionModeChat {
			modeDirs = []string{ACPStorageDir}
		} else if mode == domain.SessionModeTUI {
			modeDirs = []string{CLIStorageDir}
		}
	}
	for _, modeDir := range modeDirs {
		path := filepath.Join(home, modeDir, "conversations", id+".db")
		info, statErr := os.Stat(path)
		if statErr == nil && info.Mode().IsRegular() && info.Size() > 0 {
			db, openErr := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
			if openErr != nil {
				return false, openErr
			}
			db.SetMaxOpenConns(1)
			probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			var one int
			queryErr := db.QueryRowContext(probeCtx, "SELECT 1 FROM steps LIMIT 1").Scan(&one)
			cancel()
			_ = db.Close()
			if errors.Is(queryErr, sql.ErrNoRows) {
				continue
			}
			if queryErr != nil {
				return false, fmt.Errorf("inspect Antigravity conversation database: %w", queryErr)
			}
			return true, nil
		}
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return false, statErr
		}
	}
	return false, nil
}

func (p *Plugin) PrepareInterfaceHandoffStorage(
	ctx context.Context,
	_ ports.SessionRef,
	from, to domain.SessionMode,
	nativeConversationID string,
	env map[string]string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	home, err := GeminiHome(env)
	if err != nil {
		return err
	}
	switch {
	case from == domain.SessionModeChat && to == domain.SessionModeTUI:
		return BridgeConversationToCLI(home, nativeConversationID)
	case from == domain.SessionModeTUI && to == domain.SessionModeChat:
		return BridgeConversationToACP(home, nativeConversationID)
	default:
		return nil
	}
}

func (p *Plugin) NativeConversationID(
	ctx context.Context,
	session ports.SessionRef,
	currentMode domain.SessionMode,
	providerConversationID string,
) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	if currentMode == domain.SessionModeChat {
		id := strings.TrimSpace(providerConversationID)
		return id, id != "", nil
	}
	id := strings.TrimSpace(session.Metadata[ports.MetadataKeyAgentSessionID])
	return id, id != "", nil
}

func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(session)
	return info, ok, nil
}

func ResolveAgyBinary(ctx context.Context) (string, error) {
	return binaryutil.ResolveBinary(ctx, agyBinarySpec)
}

func (p *Plugin) agyBinary(ctx context.Context) (string, error) {
	p.binaryMu.RLock()
	cached := p.resolvedBinary
	p.binaryMu.RUnlock()
	if cached != "" {
		return cached, nil
	}

	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()
	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}

	binary, err := ResolveAgyBinary(ctx)
	if err != nil {
		return "", err
	}
	p.resolvedBinary = binary
	return binary, nil
}

func appendModelFlag(cmd *[]string, cfg ports.AgentConfig) {
	if model := strings.TrimSpace(cfg.Model); model != "" {
		*cmd = append(*cmd, "--model", model)
	}
}

func (p *Plugin) InvalidateBinaryResolution() {
	p.binaryMu.Lock()
	p.resolvedBinary = ""
	p.binaryMu.Unlock()
}
