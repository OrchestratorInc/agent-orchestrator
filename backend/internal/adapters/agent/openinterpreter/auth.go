package openinterpreter

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// AuthStatus reports local credential configuration only. Native login status
// does not validate tokens and does not cover every custom model provider.
func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	binary, err := p.ResolveBinary(ctx)
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	probe := aoprocess.CommandContext(probeCtx, binary, "login", "status")
	probe.WaitDelay = 2 * time.Second
	output, _ := probe.CombinedOutput()
	if probeCtx.Err() != nil {
		return ports.AgentAuthStatusUnknown, probeCtx.Err()
	}
	if status := authStatus(output); status != ports.AgentAuthStatusUnknown {
		return status, nil
	}

	// Native login does not report credentials supplied by a custom provider.
	// Observe only an explicitly selected user-config provider: project and
	// named-profile overrides require native configuration resolution.
	home, err := p.NativeSessionConfigDir(ctx, nil)
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	path := filepath.Join(home, "config.toml")
	info, err := os.Stat(path)
	const maxConfigBytes = 1 << 20
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxConfigBytes {
		return ports.AgentAuthStatusUnknown, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return ports.AgentAuthStatusUnknown, nil
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil || len(data) > maxConfigBytes {
		return ports.AgentAuthStatusUnknown, nil
	}
	var config struct {
		Profile        string `toml:"profile"`
		ModelProvider  string `toml:"model_provider"`
		ModelProviders map[string]struct {
			EnvKey string `toml:"env_key"`
		} `toml:"model_providers"`
	}
	if toml.Unmarshal(data, &config) != nil || config.Profile != "" || config.ModelProvider == "" {
		return ports.AgentAuthStatusUnknown, nil
	}
	provider := config.ModelProviders[config.ModelProvider]
	if provider.EnvKey != "" && strings.TrimSpace(os.Getenv(provider.EnvKey)) != "" {
		return ports.AgentAuthStatusConfigured, nil
	}
	return ports.AgentAuthStatusUnknown, nil
}

func authStatus(output []byte) ports.AgentAuthStatus {
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Logged in using ") {
			return ports.AgentAuthStatusConfigured
		}
	}
	return ports.AgentAuthStatusUnknown
}
