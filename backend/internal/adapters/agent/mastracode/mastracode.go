// Package mastracode integrates the released Mastra Code terminal CLI.
// Chat, reviewer support and interface handoff are separate, unregistered surfaces.
package mastracode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const adapterID = "mastra-code"

// Plugin adapts Mastra Code's native TUI and additive project plugin API.
type Plugin struct{ agentbase.Base }

// New constructs the adapter.
func New() *Plugin { return &Plugin{} }

var _ ports.Agent = (*Plugin)(nil)
var _ adapters.Adapter = (*Plugin)(nil)

// Manifest describes only the terminal interface.
func (*Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{ID: adapterID, Name: "Mastra Code", Description: "Run Mastra Code terminal sessions.", Version: "0.0.1", Capabilities: []adapters.Capability{adapters.CapabilityAgent}}
}

// GetConfigSpec accepts native provider-qualified model IDs.
func (*Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx, "Native provider/model ID. Configure custom providers in Mastra Code's AO profile first.")
}

var binarySpec = binaryutil.BinarySpec{
	Label: "mastracode", Names: []string{"mastracode"},
	WinNames:      []string{"mastracode.cmd", "mastracode.exe", "mastracode"},
	UnixPaths:     []string{"/usr/local/bin/mastracode", "/opt/homebrew/bin/mastracode"},
	UnixHomePaths: binaryutil.NodeManagedUnixHomePaths("mastracode"), NodeManaged: true,
	WinPaths: []binaryutil.WinPath{{Base: binaryutil.WinAppData, Parts: []string{"npm", "mastracode.cmd"}}},
}

// ResolveBinary locates the independently installed native CLI.
func (*Plugin) ResolveBinary(ctx context.Context) (string, error) {
	return binaryutil.ResolveBinary(ctx, binarySpec)
}

// AugmentRuntimeEnv isolates native application state, without redirecting HOME
// or replacing the global/project hooks, plugin registries or instructions.
func (*Plugin) AugmentRuntimeEnv(env map[string]string, dataDir string) {
	if env["MASTRA_APP_DATA_DIR"] == "" {
		env["MASTRA_APP_DATA_DIR"] = filepath.Join(dataDir, adapterID, "profile")
	}
}

func validatePolicy(mode ports.PermissionMode, allowed, denied []string) error {
	if len(allowed) != 0 || len(denied) != 0 {
		return errors.New("mastra-code does not expose AO tool allow/deny mappings")
	}
	switch ports.NormalizePermissionMode(mode) {
	case ports.PermissionModeDefault, ports.PermissionModeBypassPermissions:
		return nil
	default:
		return fmt.Errorf("mastra-code does not support permission mode %q; use native permissions or explicitly select bypass", mode)
	}
}

// GetLaunchCommand starts a new local history database, preserving older runs.
func (p *Plugin) GetLaunchCommand(ctx context.Context, cfg ports.LaunchConfig) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validatePolicy(cfg.Permissions, cfg.AllowedTools, cfg.DisallowedTools); err != nil {
		return nil, err
	}
	if err := validateTask(cfg.Prompt); err != nil {
		return nil, err
	}
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return nil, err
	}
	state, err := readState(cfg.WorkspacePath)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(state.PreviousDatabases, state.Database) {
		state.PreviousDatabases = append(state.PreviousDatabases, state.Database)
	}
	state.Database = filepath.Join(filepath.Dir(nativeStatePath(cfg.DataDir, cfg.SessionID)), "history-"+uuid.NewString()+".db")
	state.LaunchModel = strings.TrimSpace(cfg.Config.Model)
	if err := writeNativeState(cfg.DataDir, cfg.SessionID, cfg.WorkspacePath, state); err != nil {
		return nil, err
	}
	cmd := []string{binary}
	if cfg.Prompt != "" {
		cmd = append(cmd, "--tui-initial-prompt", cfg.Prompt)
	}
	return cmd, nil
}

func validateTask(prompt string) error {
	text := strings.TrimSpace(prompt)
	if strings.HasPrefix(text, "/") || strings.HasPrefix(text, "!") {
		return errors.New("Mastra Code treats tasks beginning with / or ! as native commands; start your task with a description, such as Fix /src/foo.ts, or use the terminal for native commands")
	}
	return nil
}

// PrepareRuntimeLaunch pins the effective model and permission environment after
// the manager assigns a generation. Saved native settings remain unchanged.
func (*Plugin) PrepareRuntimeLaunch(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validatePolicy(cfg.Config.Permissions, nil, nil); err != nil {
		return err
	}
	state, err := readState(cfg.WorkspacePath)
	if err != nil {
		return err
	}
	if err := validateNativeSettings(state.Profile, cfg.Config.Permissions); err != nil {
		return err
	}
	if ports.NormalizePermissionMode(cfg.Config.Permissions) != ports.PermissionModeBypassPermissions {
		if state.Profile != filepath.Join(cfg.DataDir, adapterID, "profile") {
			return errors.New("mastra-code native permission setup requires the AO-owned profile")
		}
		// The released CLI only recognizes truthy YOLO environment values;
		// "0" cannot override the SDK's yolo=true default. Use its supported
		// preference in AO's profile, preserving every unrelated native field.
		settingsPath := filepath.Join(state.Profile, "settings.json")
		settings, err := readObject(settingsPath)
		if err != nil {
			return err
		}
		preferences := map[string]json.RawMessage{}
		if raw, ok := settings["preferences"]; ok && string(raw) != "null" {
			if err := json.Unmarshal(raw, &preferences); err != nil {
				return err
			}
			if preferences == nil {
				preferences = map[string]json.RawMessage{}
			}
		}
		preferences["yolo"] = json.RawMessage("false")
		settings["preferences"], _ = json.Marshal(preferences)
		if err := os.MkdirAll(state.Profile, 0o700); err != nil {
			return err
		}
		if err := writeObject(settingsPath, settings); err != nil {
			return err
		}
	}
	// Native resumes hydrate their own model, including in-TUI model changes.
	// Only a fresh launch carries AO's configured model in the launch descriptor.
	cfg.Env["MASTRACODE_MODEL_ID"] = state.LaunchModel
	cfg.Env["MASTRA_DB_PATH"] = state.Database
	if ports.NormalizePermissionMode(cfg.Config.Permissions) == ports.PermissionModeBypassPermissions {
		cfg.Env["MASTRACODE_YOLO"] = "1"
	} else {
		// A truthy environment override must not turn a native-permissions AO
		// session into bypass. Saved yolo=true is rejected above, not rewritten.
		cfg.Env["MASTRACODE_YOLO"] = "0"
	}
	launch := cfg.Env["AO_RUNTIME_LAUNCH_ID"]
	if launch == "" {
		return errors.New("mastra-code requires a supervised runtime generation")
	}
	path := receiptPath(cfg.DataDir, cfg.SessionID, launch)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	cfg.Env["AO_MASTRA_RECEIPT_PATH"] = path
	return nil
}

func validateNativeSettings(profile string, mode ports.PermissionMode) error {
	data, err := os.ReadFile(filepath.Join(profile, "settings.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var settings struct {
		Preferences struct {
			Yolo bool `json:"yolo"`
		} `json:"preferences"`
		BackgroundTools struct {
			Enabled bool `json:"enabled"`
		} `json:"backgroundTools"`
		Signals struct {
			Schedule bool `json:"experimentalScheduleTools"`
		} `json:"signals"`
		Storage struct {
			Backend string `json:"backend"`
			Libsql  struct {
				URL string `json:"url"`
			} `json:"libsql"`
		} `json:"storage"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return fmt.Errorf("invalid native settings: %w", err)
	}
	if settings.Preferences.Yolo && ports.NormalizePermissionMode(mode) != ports.PermissionModeBypassPermissions {
		return errors.New("native Mastra yolo is enabled; disable it natively or explicitly select AO bypass")
	}
	if settings.BackgroundTools.Enabled || settings.Signals.Schedule {
		return errors.New("mastra-code background tools/schedulers are outside AO's qualified foreground ownership; disable them natively")
	}
	if settings.Storage.Backend == "pg" || settings.Storage.Libsql.URL != "" {
		return errors.New("mastra-code currently requires its local default LibSQL history")
	}
	return nil
}

// AuthStatus reports configuration, never authorization from a stored key.
func (*Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	if err := ctx.Err(); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	profile := os.Getenv("MASTRA_APP_DATA_DIR")
	if profile == "" {
		dataDir := os.Getenv("AO_DATA_DIR")
		if dataDir == "" {
			dataDir = filepath.Join(userHome(), ".ao", "data")
		}
		profile = filepath.Join(dataDir, adapterID, "profile")
	}
	data, err := os.ReadFile(filepath.Join(profile, "settings.json"))
	if errors.Is(err, os.ErrNotExist) {
		return ports.AgentAuthStatusUnknown, nil
	}
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	var settings struct {
		Providers []struct {
			APIKey string `json:"apiKey"`
		} `json:"customProviders"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return ports.AgentAuthStatusUnknown, errors.New("invalid native Mastra settings")
	}
	for _, provider := range settings.Providers {
		if strings.TrimSpace(provider.APIKey) != "" {
			return ports.AgentAuthStatusConfigured, nil
		}
	}
	return ports.AgentAuthStatusUnknown, nil
}

// SessionInfo exposes real native IDs captured by managed lifecycle callbacks.
func (*Plugin) SessionInfo(ctx context.Context, session ports.SessionRef) (ports.SessionInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionInfo{}, false, err
	}
	info, ok := agentbase.StandardSessionInfo(session)
	return info, ok && nativeIDPattern.MatchString(info.AgentSessionID), nil
}
