package claudecode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/nativeconfig"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const claudeConfigDirEnv = "CLAUDE_CONFIG_DIR"

var (
	_ ports.AgentContinuationCapabilityProvider = (*Plugin)(nil)
	_ ports.AgentFreshNativeSessionIDProvider   = (*Plugin)(nil)
	_ ports.AgentNativeSessionConfigProvider    = (*Plugin)(nil)
	_ ports.AgentNativeSessionProber            = (*Plugin)(nil)
	_ ports.AgentTranscriptLocator              = (*Plugin)(nil)
)

func (p *Plugin) ContinuationCapabilities() ports.ContinuationCapabilities {
	return ports.ContinuationCapabilities{
		FreshNativeSessionID: ports.FreshNativeSessionIDCallerAssigned,
	}
}

func (p *Plugin) NewNativeSessionID() string {
	return uuid.NewString()
}

func (p *Plugin) NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dir, err := nativeconfig.Resolve(env, claudeConfigDirEnv, ".claude")
	if err != nil {
		return "", fmt.Errorf("claude-code: resolve config dir: %w", err)
	}
	return dir, nil
}

func (p *Plugin) ProbeNativeSession(ctx context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	if strings.TrimSpace(ref.ConfigDir) == "" {
		return ports.NativeSessionAvailabilityUnknown, nil
	}
	_, ok, err := p.LocateTranscript(ctx, ref)
	if err != nil {
		return ports.NativeSessionAvailabilityUnknown, err
	}
	if ok {
		return ports.NativeSessionAvailabilityAvailable, nil
	}
	return ports.NativeSessionAvailabilityUnavailable, nil
}

func (p *Plugin) LocateTranscript(ctx context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	sessionID, err := canonicalClaudeNativeSessionID(ref.NativeSessionID)
	if err != nil {
		return "", false, err
	}
	configDir := strings.TrimSpace(ref.ConfigDir)
	if configDir == "" {
		return "", false, nil
	}

	projectsDir := filepath.Join(configDir, "projects")
	projects, err := os.ReadDir(projectsDir)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("claude-code: read transcript projects: %w", err)
	}
	for _, project := range projects {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		if !project.IsDir() {
			continue
		}
		path := filepath.Join(projectsDir, project.Name(), sessionID+".jsonl")
		info, err := os.Stat(path)
		switch {
		case err == nil && info.Mode().IsRegular():
			return path, true, nil
		case errors.Is(err, os.ErrNotExist):
			continue
		case err != nil:
			return "", false, fmt.Errorf("claude-code: stat transcript: %w", err)
		}
	}
	return "", false, nil
}

func canonicalClaudeNativeSessionID(value string) (string, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", fmt.Errorf("claude-code: invalid native session id: %w", err)
	}
	return parsed.String(), nil
}
