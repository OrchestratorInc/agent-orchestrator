package memcode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/modelcatalog"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/terminalui"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

type Plugin struct{ agentbase.Base }

func New() *Plugin { return &Plugin{} }

func (*Plugin) ExitDetectionMode() ports.AgentExitDetectionMode {
	return ports.AgentExitDetectionSupervisor
}

func (*Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{ID: "memcode", Name: "memcode", Description: "Official memcode terminal sessions", Version: "0.0.1", Capabilities: []adapters.Capability{adapters.CapabilityAgent}}
}

var binarySpec = binaryutil.BinarySpec{Label: "memcode", Names: []string{"memcode"}, WinNames: []string{"memcode.exe"}, UnixHomePaths: [][]string{{".local", "bin", "memcode"}}, ValidateIdentity: func(ctx context.Context, path string) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := process.CommandContext(ctx, path, "--version")
	cmd.Env = append(os.Environ(), "MEMCODE_AUTO_UPDATE=off")
	b, err := cmd.CombinedOutput()
	return err == nil && strings.Contains(string(b), "memcode")
}}

func (*Plugin) ResolveBinary(ctx context.Context) (string, error) {
	return binaryutil.ResolveBinary(ctx, binarySpec)
}

func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	if cfg.Config.Mode != "" || cfg.Config.Effort != "" {
		return nil, errors.New("memcode mode/effort overrides are unsupported")
	}
	if len(cfg.AllowedTools) > 0 || len(cfg.DisallowedTools) > 0 {
		return nil, errors.New("memcode per-launch tool restrictions are unsupported")
	}
	if err := validateModel(ctx, cfg.WorkspacePath, cfg.Config.Model); err != nil {
		return nil, err
	}
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return nil, err
	}
	args := []string{binary, "run"}
	switch cfg.Permissions {
	case ports.PermissionModeAcceptEdits:
		return nil, errors.New("memcode does not support accept-edits")
	case ports.PermissionModeAuto:
		args = append(args, "--auto")
	case ports.PermissionModeBypassPermissions:
		args = append(args, "--allow-all")
	default:
		args = append(args, "--ask")
	}
	return args, nil
}

func (*Plugin) GetPromptDeliveryStrategy(ctx context.Context, _ ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	return ports.PromptDeliveryAfterStart, ctx.Err()
}

func (*Plugin) GetRestoreCommand(context.Context, ports.RestoreConfig) ([]string, bool, error) {
	return nil, false, errors.New("memcode interactive native restore initialization required")
}

func (*Plugin) SessionInfo(ctx context.Context, s ports.SessionRef) (ports.SessionInfo, bool, error) {
	info, ok := agentbase.StandardSessionInfo(s)
	return info, ok, ctx.Err()
}

func (*Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx, "Memcode selects the chosen model in its native TUI and remembers it for this project. Custom endpoints keep their own model selection.")
}

func validateModel(ctx context.Context, workspace, model string) error {
	if model == "" {
		return nil
	}
	customEndpoint, err := UsesCustomEndpoint(ctx, workspace, nil)
	if err != nil {
		return err
	}
	if customEndpoint {
		return errors.New("memcode custom endpoint models must be selected in the native TUI")
	}
	for _, choice := range modelcatalog.Base("memcode").Models {
		if model == choice.ID {
			return nil
		}
	}
	return errors.New("memcode model is not in the supported native catalog")
}

func UsesCustomEndpoint(ctx context.Context, workspace string, env map[string]string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if env["MEMCODE_ENDPOINT_URL"] != "" || os.Getenv("MEMCODE_ENDPOINT_URL") != "" {
		return true, nil
	}
	settings, err := readMemcodeAuthEnv(memcodeAuthEnvPath())
	if err != nil {
		return false, err
	}
	if settings["MEMCODE_ENDPOINT_URL"] != "" {
		return true, nil
	}
	if workspace != "" {
		data, err := os.ReadFile(filepath.Join(workspace, ".memcode", "config.json"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, err
		}
		if len(data) > 0 {
			var native struct {
				Endpoints []json.RawMessage `json:"endpoints"`
			}
			if err := json.Unmarshal(data, &native); err != nil {
				return false, fmt.Errorf("read memcode model configuration: %w", err)
			}
			if len(native.Endpoints) > 0 {
				return true, nil
			}
		}
	}
	return false, nil
}

func (*Plugin) AfterStartCommand(ctx context.Context, cfg ports.LaunchConfig) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := validateModel(ctx, cfg.WorkspacePath, cfg.Config.Model); err != nil {
		return "", err
	}
	if cfg.Config.Model == "" {
		return "", nil
	}
	return "/model " + cfg.Config.Model, nil
}

func (*Plugin) AfterStartCommandApplied(cfg ports.LaunchConfig, output string) bool {
	model := cfg.Config.Model
	if model == "" || !emptyComposer(output) {
		return false
	}
	confirmed := ""
	for _, line := range strings.Split(terminalui.PlainTerminalText(output), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "model → ") {
			confirmed = ""
			if fields := strings.Fields(strings.TrimPrefix(line, "model → ")); len(fields) > 0 {
				confirmed = fields[0]
			}
		}
	}
	return confirmed == model
}
