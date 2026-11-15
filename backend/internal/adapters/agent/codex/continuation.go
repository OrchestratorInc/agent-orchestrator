package codex

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/nativeconfig"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const codexHomeEnv = "CODEX_HOME"

var (
	_ ports.AgentContinuationCapabilityProvider = (*Plugin)(nil)
	_ ports.AgentNativeSessionConfigProvider    = (*Plugin)(nil)
	_ ports.AgentNativeSessionProber            = (*Plugin)(nil)
	_ ports.AgentTranscriptLocator              = (*Plugin)(nil)
)

func (p *Plugin) ContinuationCapabilities() ports.ContinuationCapabilities {
	return ports.ContinuationCapabilities{
		FreshNativeSessionID: ports.FreshNativeSessionIDProviderAssigned,
	}
}

func (p *Plugin) NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dir, err := nativeconfig.Resolve(env, codexHomeEnv, ".codex")
	if err != nil {
		return "", fmt.Errorf("codex: resolve config dir: %w", err)
	}
	return dir, nil
}

func (p *Plugin) ProbeNativeSession(ctx context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	if strings.TrimSpace(ref.ConfigDir) == "" {
		return ports.NativeSessionAvailabilityUnknown, nil
	}
	sessionID, err := validateCodexNativeSessionID(ref.NativeSessionID)
	if err != nil {
		return ports.NativeSessionAvailabilityUnknown, err
	}
	_, ok, err := findCodexRollout(ctx, filepath.Join(ref.ConfigDir, "sessions"), sessionID, codexRolloutStored)
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
	sessionID, err := validateCodexNativeSessionID(ref.NativeSessionID)
	if err != nil {
		return "", false, err
	}
	configDir := strings.TrimSpace(ref.ConfigDir)
	if configDir == "" {
		return "", false, nil
	}
	for _, dir := range []string{"sessions", "archived_sessions"} {
		path, ok, err := findCodexTranscript(ctx, filepath.Join(configDir, dir), sessionID)
		if err != nil {
			return "", false, err
		}
		if ok {
			return path, true, nil
		}
	}
	return "", false, nil
}

func findCodexTranscript(ctx context.Context, root, sessionID string) (string, bool, error) {
	return findCodexRollout(ctx, root, sessionID, func(name, id string) bool { return strings.HasSuffix(name, "-"+id+".jsonl") })
}

func codexRolloutStored(name, id string) bool {
	thread := nativeconfig.CodexRolloutThread(name)
	return thread != "" && strings.EqualFold(thread, id)
}

func findCodexRollout(ctx context.Context, root, sessionID string, match func(name, id string) bool) (string, bool, error) {
	path, found, err := nativeconfig.FindFile(ctx, root, func(name string) bool { return match(name, sessionID) })
	if err != nil {
		return "", false, fmt.Errorf("codex: scan transcripts: %w", err)
	}
	return path, found, nil
}

func validateCodexNativeSessionID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 256 || strings.ContainsAny(value, `/\\`+"\x00") {
		return "", fmt.Errorf("codex: invalid native session id")
	}
	return value, nil
}
