// Package memcode integrates the official memcode TUI. Native interactive
// /resume is deliberately separate from its broken mount-time --resume route.
package memcode

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type Plugin struct{ agentbase.Base }

func New() *Plugin { return &Plugin{} }
func (*Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{ID: "memcode", Name: "memcode", Description: "Official memcode terminal sessions", Version: "0.0.1", Capabilities: []adapters.Capability{adapters.CapabilityAgent}}
}

var binarySpec = binaryutil.BinarySpec{Label: "memcode", Names: []string{"memcode"}, WinNames: []string{"memcode.exe"}, UnixHomePaths: [][]string{{".local", "bin", "memcode"}}, ValidateIdentity: func(ctx context.Context, path string) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
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
	if err := validateModel(cfg.WorkspacePath, cfg.Config.Model); err != nil {
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
func (*Plugin) GetPromptDeliveryStrategy(context.Context, ports.LaunchConfig) (ports.PromptDeliveryStrategy, error) {
	return ports.PromptDeliveryAfterStart, nil
}
func (*Plugin) GetRestoreCommand(context.Context, ports.RestoreConfig) ([]string, bool, error) {
	return nil, false, errors.New("memcode interactive native restore initialization required")
}
func (*Plugin) SessionInfo(ctx context.Context, s ports.SessionRef) (ports.SessionInfo, bool, error) {
	info, ok := agentbase.StandardSessionInfo(s)
	return info, ok, ctx.Err()
}
func (*Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx, "Must match the selected native endpoint's remembered model; the TUI ignores --model.")
}
func (*Plugin) AuthStatus(context.Context) (ports.AgentAuthStatus, error) {
	if os.Getenv("MEMCODE_ENDPOINT_URL") != "" {
		return ports.AgentAuthStatusConfigured, nil
	}
	return ports.AgentAuthStatusUnknown, nil
}
func validateModel(workspace, model string) error {
	if strings.TrimSpace(model) == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(workspace, ".memcode", "config.json"))
	if err != nil {
		return errors.New("memcode model override requires a configured native endpoint")
	}
	var config struct {
		Endpoint  string `json:"endpoint"`
		Endpoints []struct {
			Name      string `json:"name"`
			BaseURL   string `json:"base_url"`
			LastModel string `json:"last_model"`
		} `json:"endpoints"`
	}
	if err = json.Unmarshal(data, &config); err != nil {
		return err
	}
	url := os.Getenv("MEMCODE_ENDPOINT_URL")
	for i, e := range config.Endpoints {
		if (url != "" && strings.TrimRight(e.BaseURL, "/") == strings.TrimRight(url, "/")) || (url == "" && (e.Name == config.Endpoint || config.Endpoint == "" && i == 0)) {
			if e.LastModel == model {
				return nil
			}
			return errors.New("memcode model must match native endpoint last_model; use native configuration before launch")
		}
	}
	return errors.New("memcode selected endpoint/model cannot be verified")
}
