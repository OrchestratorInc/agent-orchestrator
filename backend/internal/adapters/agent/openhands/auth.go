package openhands

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.AgentAuthChecker = (*Plugin)(nil)

func (p *Plugin) AuthStatus(ctx context.Context) (ports.AgentAuthStatus, error) {
	if _, err := p.ResolveBinary(ctx); err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	path := settingsPath()
	status, err := settingsAuthStatus(path)
	if status == ports.AgentAuthStatusAuthorized {
		ports.RecordAuthEvidence(ctx, authprobe.FileSource(path))
	}
	return status, err
}

func settingsPath() string {
	dir := strings.TrimSpace(os.Getenv("OPENHANDS_PERSISTENCE_DIR"))
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		dir = filepath.Join(home, ".openhands")
	}
	return filepath.Join(dir, "agent_settings.json")
}

func settingsAuthStatus(path string) (ports.AgentAuthStatus, error) {
	if path == "" {
		return ports.AgentAuthStatusUnknown, nil
	}
	data, err := os.ReadFile(path) //nolint:gosec // fixed OpenHands settings location
	if errors.Is(err, os.ErrNotExist) {
		return ports.AgentAuthStatusUnauthorized, nil
	}
	if err != nil {
		return ports.AgentAuthStatusUnknown, err
	}
	var settings struct {
		LLM struct {
			Model string `json:"model"`
		} `json:"llm"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return ports.AgentAuthStatusUnknown, nil //nolint:nilerr // malformed settings → can't determine auth status
	}
	if strings.TrimSpace(settings.LLM.Model) == "" {
		return ports.AgentAuthStatusUnauthorized, nil
	}
	return ports.AgentAuthStatusAuthorized, nil
}
