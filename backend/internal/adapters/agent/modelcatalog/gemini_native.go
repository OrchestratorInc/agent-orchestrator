package modelcatalog

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type geminiOptionsProbe func(context.Context, acpdriver.Launch, string, *slog.Logger) ([]ports.ChatConfigOption, error)

// DiscoverGeminiOptions reads native ACP choices with discovery-only startup settings.
func DiscoverGeminiOptions(ctx context.Context, request ports.AgentModelDiscoveryRequest, log *slog.Logger) ([]ports.ChatConfigOption, error) {
	return discoverGeminiOptions(ctx, request, log, acpdriver.DiscoverConfigOptions)
}

func discoverGeminiOptions(ctx context.Context, request ports.AgentModelDiscoveryRequest, log *slog.Logger, probe geminiOptionsProbe) ([]ports.ChatConfigOption, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	systemPath, defaultsPath := geminiSystemPaths(request.Env)
	if !filepath.IsAbs(systemPath) {
		systemPath = filepath.Join(request.WorkingDir, systemPath)
	}
	raw, err := readModelConfig(systemPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("gemini discovery cannot preserve system settings: %w", err)
	}
	settings := map[string]json.RawMessage{}
	if err == nil {
		if json.Unmarshal(raw, &settings) != nil || settings == nil {
			return nil, fmt.Errorf("gemini discovery system settings must be a JSON object")
		}
	}
	for _, path := range [][]string{
		{"experimental", "autoMemory"},
		{"hooksConfig", "enabled"},
		{"admin", "mcp", "enabled"},
		{"admin", "extensions", "enabled"},
	} {
		if err := setGeminiDiscoveryFalse(settings, path); err != nil {
			return nil, err
		}
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		return nil, fmt.Errorf("gemini discovery encode system settings: %w", err)
	}
	dataDir := geminiDiscoveryEnv(request.Env, "AO_DATA_DIR")
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dataDir = filepath.Join(home, ".ao")
	}
	if !filepath.IsAbs(dataDir) {
		return nil, fmt.Errorf("gemini discovery data directory must be absolute")
	}
	scratchRoot := filepath.Join(dataDir, "model-discovery")
	if err := os.MkdirAll(scratchRoot, 0o700); err != nil {
		return nil, fmt.Errorf("gemini discovery create scratch directory: %w", err)
	}
	scratch, err := os.MkdirTemp(scratchRoot, "gemini-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	clonePath := filepath.Join(scratch, "system-settings.json")
	if err := os.WriteFile(clonePath, encoded, 0o600); err != nil {
		return nil, fmt.Errorf("gemini discovery write settings clone: %w", err)
	}
	env := make(map[string]string, len(request.Env)+2)
	for key, value := range request.Env {
		env[key] = value
	}
	env["GEMINI_CLI_SYSTEM_SETTINGS_PATH"] = clonePath
	env["GEMINI_CLI_SYSTEM_DEFAULTS_PATH"] = defaultsPath
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return probe(ctx, acpdriver.Launch{Command: request.Binary, Args: []string{"--acp", "--extensions", "none"}, Env: env}, request.WorkingDir, log)
}

func setGeminiDiscoveryFalse(settings map[string]json.RawMessage, path []string) error {
	if len(path) == 1 {
		settings[path[0]] = json.RawMessage("false")
		return nil
	}
	child := map[string]json.RawMessage{}
	if raw, exists := settings[path[0]]; exists {
		if json.Unmarshal(raw, &child) != nil || child == nil {
			return fmt.Errorf("gemini discovery cannot preserve non-object setting %q", path[0])
		}
	}
	if err := setGeminiDiscoveryFalse(child, path[1:]); err != nil {
		return err
	}
	encoded, err := json.Marshal(child)
	if err != nil {
		return err
	}
	settings[path[0]] = encoded
	return nil
}

func geminiSystemPaths(env map[string]string) (string, string) {
	system := geminiDiscoveryEnv(env, "GEMINI_CLI_SYSTEM_SETTINGS_PATH")
	if system == "" {
		switch runtime.GOOS {
		case "darwin":
			system = "/Library/Application Support/GeminiCli/settings.json"
		case "windows":
			system = `C:\ProgramData\gemini-cli\settings.json`
		default:
			system = "/etc/gemini-cli/settings.json"
		}
	}
	defaults := geminiDiscoveryEnv(env, "GEMINI_CLI_SYSTEM_DEFAULTS_PATH")
	if defaults == "" {
		defaults = filepath.Join(filepath.Dir(system), "system-defaults.json")
	}
	return system, defaults
}

func geminiDiscoveryEnv(env map[string]string, key string) string {
	if value, exists := env[key]; exists {
		return strings.TrimSpace(value)
	}
	return strings.TrimSpace(os.Getenv(key))
}
