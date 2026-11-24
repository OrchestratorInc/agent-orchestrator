// Package dockeragent provides metadata for the user-installed Docker Agent.
// Only its native ACP Chat interface is supported by AO.
package dockeragent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/adapters"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/adapters/agent/agentbase"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/adapters/agent/binaryutil"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/process"
)

// ConfigEnv selects an existing native YAML configuration. AO never rewrites it.
// Without it, AO generates a private default configuration per session.
const ConfigEnv = "AO_DOCKER_AGENT_CONFIG"

// ErrNoProviderKey reports that AO's default config has no provider key it
// can use and no native config is selected.
var ErrNoProviderKey = errors.New("docker agent needs a provider key")

// Plugin supplies installation discovery and configuration metadata for Chat.
type Plugin struct{ agentbase.Base }

// New returns the Docker Agent metadata plugin.
func New() *Plugin { return &Plugin{} }

var _ adapters.Adapter = (*Plugin)(nil)
var _ ports.Agent = (*Plugin)(nil)
var _ ports.AgentBinaryResolver = (*Plugin)(nil)
var _ ports.AgentAuthChecker = (*Plugin)(nil)

// Manifest describes the native standalone harness.
func (*Plugin) Manifest() adapters.Manifest {
	return adapters.Manifest{ID: "docker-agent", Name: "Docker Agent", Description: "User-installed Docker Agent, native ACP Chat only.", Version: "0.0.1", Capabilities: []adapters.Capability{adapters.CapabilityAgent}}
}

// GetConfigSpec exposes native configured model choices, not arbitrary model IDs.
func (*Plugin) GetConfigSpec(ctx context.Context) (ports.ConfigSpec, error) {
	return agentbase.ModelConfigSpec(ctx, "Native ACP model option (default or model:<configured-name>); configuration selected with AO_DOCKER_AGENT_CONFIG.")
}

// GetLaunchCommand refuses terminal execution rather than fabricating a TUI.
func (*Plugin) GetLaunchCommand(ctx context.Context, _ ports.LaunchConfig) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("docker agent is available in Chat mode only")
}

// ResolveBinary selects the standalone binary; Docker Desktop is not required.
func (*Plugin) ResolveBinary(ctx context.Context) (string, error) {
	return binaryutil.ResolveBinary(ctx, binaryutil.BinarySpec{Label: "Docker Agent", Names: []string{"docker-agent"}, WinNames: []string{"docker-agent.exe"}, UnixPaths: []string{"/usr/local/bin/docker-agent", "/opt/homebrew/bin/docker-agent", "/home/linuxbrew/.linuxbrew/bin/docker-agent"}, UnixHomePaths: [][]string{{".local", "bin", "docker-agent"}}})
}

// AuthStatus asks Docker Agent's own credential diagnosis (`doctor --json`),
// which sees every source Docker Agent reads: the process environment and the
// env file `docker-agent setup` writes. It reports configuration, not remote
// authorization; the provider checks the key on a live turn. Releases without
// `doctor` fall back to AO's own key lookup.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	config := strings.TrimSpace(os.Getenv(ConfigEnv))
	if status, ok := doctorAuthStatus(ctx, binary, config); ok {
		if status == ports.AgentAuthStatusConfigured && config == "" {
			return verifyDefaultProvider(ctx, nil), nil
		}
		return status, nil
	}
	if config != "" {
		return ports.AgentAuthStatusConfigured, nil
	}
	if len(DefaultModels(nil)) > 0 {
		return verifyDefaultProvider(ctx, nil), nil
	}
	return ports.AgentAuthStatusUnknown, nil
}

const doctorTimeout = 15 * time.Second

// doctorReport is the subset of `docker-agent doctor --json` AO reads. It
// never carries secret values.
type doctorReport struct {
	Providers []struct {
		Provider string `json:"provider"`
		Found    bool   `json:"found"`
	} `json:"providers"`
	AutoModel *struct {
		Usable bool `json:"usable"`
	} `json:"auto_model"`
}

// doctorAuthStatus runs Docker Agent's diagnosis. With a selected config it
// checks that file's requirements (doctor exits non-zero when an issue would
// stop the agent); otherwise AO's default config needs a provider it can
// render. ok is false when doctor is unavailable or its output unreadable.
func doctorAuthStatus(ctx context.Context, binary, config string) (ports.AgentAuthStatus, bool) {
	ctx, cancel := context.WithTimeout(ctx, doctorTimeout)
	defer cancel()
	args := []string{"doctor", "--json"}
	if config != "" {
		args = []string{"doctor", config, "--json"}
	}
	out, runErr := process.CommandContext(ctx, binary, args...).Output()
	var report doctorReport
	if err := json.Unmarshal(out, &report); err != nil || report.Providers == nil {
		return ports.AgentAuthStatusUnknown, false
	}
	if config != "" {
		if runErr != nil {
			return ports.AgentAuthStatusUnauthorized, true
		}
		return ports.AgentAuthStatusConfigured, true
	}
	for _, provider := range report.Providers {
		if provider.Found && defaultConfigProvider(provider.Provider) {
			return ports.AgentAuthStatusConfigured, true
		}
	}
	return ports.AgentAuthStatusUnauthorized, true
}

func defaultConfigProvider(name string) bool {
	for _, entry := range providerModels {
		if entry.provider == name {
			return true
		}
	}
	return false
}

// providerModels are the models offered per provider key: Docker Agent's own
// suggestions, with Google's -latest aliases so Gemini defaults do not retire.
// The first available model becomes the default; all are offered.
var providerModels = []struct {
	keys     []string
	provider string
	models   []string
}{
	{[]string{"ANTHROPIC_API_KEY"}, "anthropic", []string{"claude-sonnet-4-6"}},
	{[]string{"OPENAI_API_KEY"}, "openai", []string{"gpt-5.6", "gpt-5.6-luna"}},
	{[]string{"GOOGLE_API_KEY", "GEMINI_API_KEY"}, "google", []string{"gemini-flash-lite-latest", "gemini-flash-latest"}},
}

// DefaultModel is one entry of AO's default Docker Agent configuration.
type DefaultModel struct{ Name, Provider, Model string }

// lookupKey reads key from env, falling back to the process environment and
// then to the env file `docker-agent setup` stores keys in.
func lookupKey(env map[string]string, key string) string {
	value, ok := env[key]
	if !ok {
		value = os.Getenv(key)
	}
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return setupEnvFileValue(key)
}

// setupEnvFile is where `docker-agent setup` saves provider keys
// ($XDG_CONFIG_HOME/cagent/.env, default ~/.config/cagent/.env).
func setupEnvFile() string {
	base := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "cagent", ".env")
}

// setupEnvFileValue reads one KEY=VALUE entry from the setup env file. A
// missing or unreadable file contributes nothing.
func setupEnvFileValue(key string) string {
	path := setupEnvFile()
	if path == "" {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
		name, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(name) != key {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		return strings.TrimSpace(value)
	}
	return ""
}

// ProviderEnv returns launch overlays for keys AO accepts under another name:
// Docker Agent's google provider reads GOOGLE_API_KEY, so a GEMINI_API_KEY
// alone is passed on under that name.
func ProviderEnv(env map[string]string) map[string]string {
	overlay := map[string]string{}
	if lookupKey(env, "GOOGLE_API_KEY") == "" {
		if gemini := lookupKey(env, "GEMINI_API_KEY"); gemini != "" {
			overlay["GOOGLE_API_KEY"] = gemini
		}
	}
	return overlay
}

// DefaultModels lists the models AO's default configuration can use with the
// provider keys present in env (falling back to the process environment).
// Providers whose key env itself supplies (the session's project or
// Orchestrator-saved keys) come first, so the first model, which the default
// config launches with, uses the session's key instead of a daemon-wide one.
func DefaultModels(env map[string]string) []DefaultModel {
	var session, inherited []DefaultModel
	for _, entry := range providerModels {
		available, fromSession := false, false
		for _, key := range entry.keys {
			available = available || lookupKey(env, key) != ""
			fromSession = fromSession || strings.TrimSpace(env[key]) != ""
		}
		if !available {
			continue
		}
		for _, model := range entry.models {
			m := DefaultModel{Name: model, Provider: entry.provider, Model: model}
			if fromSession {
				session = append(session, m)
			} else {
				inherited = append(inherited, m)
			}
		}
	}
	return append(session, inherited...)
}

// DefaultConfig renders AO's Docker Agent configuration for users who have
// not selected their own with AO_DOCKER_AGENT_CONFIG.
func DefaultConfig(env map[string]string) ([]byte, error) {
	models := DefaultModels(env)
	if len(models) == 0 {
		return nil, fmt.Errorf("%w (ANTHROPIC_API_KEY, OPENAI_API_KEY, GOOGLE_API_KEY or GEMINI_API_KEY, in the environment or saved by `docker-agent setup`) or %s", ErrNoProviderKey, ConfigEnv)
	}
	var b strings.Builder
	b.WriteString("# Generated by Agent Orchestrator. Set " + ConfigEnv + " to use your own.\nmodels:\n")
	for _, m := range models {
		fmt.Fprintf(&b, "  %s:\n    provider: %s\n    model: %s\n", m.Name, m.Provider, m.Model)
	}
	fmt.Fprintf(&b, "agents:\n  root:\n    model: %s\n    description: Coding assistant\n", models[0].Name)
	b.WriteString("    instruction: You are a coding assistant. Work in the session working directory.\n")
	b.WriteString("    toolsets:\n      - type: filesystem\n      - type: shell\n")
	return []byte(b.String()), nil
}
