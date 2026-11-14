package cline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
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
var _ ports.AgentNativeRestoreCommand = (*Plugin)(nil)
var _ ports.ContinuousTerminalActivityDetector = (*Plugin)(nil)

func (p *Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{
		ID:          "cline",
		Name:        "Cline",
		Description: "Run Cline worker sessions.",
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
				Description: "Model override passed to `cline --model`.",
			},
		},
	}, nil
}

func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) (cmd []string, err error) {
	binary, err := p.clineBinary(ctx)
	if err != nil {
		return nil, err
	}

	cmd = []string{binary}
	appendApprovalFlags(&cmd, cfg.Permissions)
	appendModelFlag(&cmd, cfg.Config)

	if cfg.SystemPrompt != "" {
		cmd = append(cmd, "-s", cfg.SystemPrompt)
	}

	return cmd, nil
}

func (p *Plugin) GetPromptDeliveryStrategy(ctx context.Context, _ ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return ports.PromptDeliveryAfterStart, nil
}

func (p *Plugin) PromptReadinessHints(ctx context.Context, _ ports.LaunchConfig) (ports.PromptReadinessHints, error) {
	if err := ctx.Err(); err != nil {
		return ports.PromptReadinessHints{}, err
	}
	return ports.PromptReadinessHints{
		InitialDelay: 750 * time.Millisecond,
		Patterns: []string{
			"Type a message",
			"What can I help",
			">",
		},
		PollInterval: 200 * time.Millisecond,
		Timeout:      8 * time.Second,
		Lines:        80,
	}, nil
}

func (p *Plugin) GetRestoreCommand(ctx context.Context, cfg ports.RestoreConfig) (cmd []string, ok bool, err error) {
	cmd, _, ok, err = p.GetRestoreCommandWithSessionID(ctx, cfg)
	return cmd, ok, err
}

func (p *Plugin) GetRestoreCommandWithSessionID(ctx context.Context, cfg ports.RestoreConfig) (cmd []string, agentSessionID string, ok bool, err error) {
	if cfg.Session.Metadata[ports.MetadataKeyImportSourceNativeID] != "" {
		return p.importedRestoreCommand(ctx, cfg)
	}
	if err := ctx.Err(); err != nil {
		return nil, "", false, err
	}
	binary, err := p.clineBinary(ctx)
	if err != nil {
		return nil, "", false, err
	}

	agentSessionID = strings.TrimSpace(cfg.Session.Metadata[ports.MetadataKeyAgentSessionID])
	if strings.TrimSpace(cfg.Session.WorkspacePath) != "" {
		var savedHistory bool
		if agentSessionID != "" {
			savedHistory, _ = p.NativeConversationExists(ctx, cfg.Session, agentSessionID, nil)
		}
		if !savedHistory {
			recoveredID, historyErr := clineHistorySessionID(ctx, binary, cfg.Session.WorkspacePath, agentSessionID)
			if historyErr == nil {
				agentSessionID = recoveredID
			} else if ctx.Err() != nil {
				return nil, "", false, ctx.Err()
			} else {
				slog.Default().WarnContext(ctx, "cline: history lookup failed; skipping recovery",
					"sessionID", cfg.Session.ID, "error", historyErr)
			}
		}
	}
	if agentSessionID == "" {
		return nil, "", false, nil
	}

	cmd = make([]string, 0, 8)
	cmd = append(cmd, binary)
	appendApprovalFlags(&cmd, cfg.Permissions)
	appendModelFlag(&cmd, cfg.Config)
	if cfg.SystemPrompt != "" {
		cmd = append(cmd, "-s", cfg.SystemPrompt)
	}
	cmd = append(cmd, "--id", agentSessionID)
	return cmd, agentSessionID, true, nil
}

func clineHistorySessionID(ctx context.Context, binary, workspacePath, storedID string) (string, error) {
	cmd := aoprocess.CommandContext(ctx, binary, "history", "--json", "--limit", "100") //nolint:gosec // binary is adapter-resolved; args are static
	cmd.Dir = workspacePath
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("cline: read session history: %w", err)
	}

	var history []struct {
		SessionID string `json:"sessionId"`
		CWD       string `json:"cwd"`
	}
	if err := json.Unmarshal(out, &history); err != nil {
		return "", fmt.Errorf("cline: parse session history: %w", err)
	}
	want := filepath.Clean(workspacePath)
	var newestID string
	for _, entry := range history {
		id := strings.TrimSpace(entry.SessionID)
		if filepath.Clean(strings.TrimSpace(entry.CWD)) == want && id != "" && len(id) <= maxHookSessionIDLen {
			if id == storedID {
				return id, nil
			}
			if newestID == "" {
				newestID = id
			}
		}
	}
	return newestID, nil
}

func (p *Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(session)
	return info, ok, nil
}

var clineBinarySpec = binaryutil.BinarySpec{
	Label:         "cline",
	Names:         []string{"cline"},
	WinNames:      []string{"cline.cmd", "cline.exe", "cline"},
	UnixPaths:     []string{"/usr/local/bin/cline", "/opt/homebrew/bin/cline"},
	UnixHomePaths: binaryutil.NodeManagedUnixHomePaths("cline"),
	NodeManaged:   true,
	WinPaths: []binaryutil.WinPath{
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "cline.cmd"}},
		{Base: binaryutil.WinAppData, Parts: []string{"npm", "cline.exe"}},
	},
}

func ResolveClineBinary(ctx context.Context) (string, error) {
	return binaryutil.ResolveBinary(ctx, clineBinarySpec)
}

func (p *Plugin) clineBinary(ctx context.Context) (string, error) {
	p.binaryMu.Lock()
	defer p.binaryMu.Unlock()

	if p.resolvedBinary != "" {
		return p.resolvedBinary, nil
	}

	binary, err := ResolveClineBinary(ctx)
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

func appendApprovalFlags(cmd *[]string, permissions ports.PermissionMode) {
	switch ports.NormalizePermissionMode(permissions) {
	case ports.PermissionModeDefault:
	case ports.PermissionModeAcceptEdits:
		*cmd = append(*cmd, "--auto-approve", "true")
	case ports.PermissionModeAuto:
		*cmd = append(*cmd, "--auto-approve", "true")
	case ports.PermissionModeBypassPermissions:
		*cmd = append(*cmd, "--yolo")
	}
}

func (p *Plugin) InvalidateBinaryResolution() {
	p.binaryMu.Lock()
	p.resolvedBinary = ""
	p.binaryMu.Unlock()
}
