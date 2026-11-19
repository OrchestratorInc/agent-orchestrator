package pi

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/nativeconfig"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const piCodingAgentDirEnv = "PI_CODING_AGENT_DIR"

var (
	_ ports.AgentNativeSessionConfigProvider = (*Plugin)(nil)
	_ ports.AgentNativeSessionProber         = (*Plugin)(nil)
	_ ports.AgentTranscriptLocator           = (*Plugin)(nil)
)

// NativeSessionConfigDir returns Pi's session-state root. A project environment
// may explicitly select PI_CODING_AGENT_DIR; otherwise Pi uses ~/.pi/agent.
func (p *Plugin) NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	dir, err := nativeconfig.Resolve(env, piCodingAgentDirEnv, filepath.Join(".pi", "agent"))
	if err != nil {
		return "", fmt.Errorf("pi: resolve config dir: %w", err)
	}
	return dir, nil
}

// ProbeNativeSession uses Pi's local JSONL transcript as authoritative backing
// state for pi-acp's session/load. Pi stores one <time>_<session-id>.jsonl
// below a per-cwd directory in <config>/sessions.
func (p *Plugin) ProbeNativeSession(ctx context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	return nativeconfig.ProbeTranscript(ctx, ref, p.LocateTranscript)
}

// LocateTranscript finds Pi's provider-owned JSONL by native session id. pi-acp
// resolves a session by id across every cwd folder, so this does too rather
// than depending on the lossy dashed-path directory name.
func (p *Plugin) LocateTranscript(ctx context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	sessionID, err := canonicalPiNativeSessionID(ref.NativeSessionID)
	if err != nil {
		return "", false, err
	}
	configDir := strings.TrimSpace(ref.ConfigDir)
	if configDir == "" {
		return "", false, nil
	}
	suffix := "_" + sessionID + ".jsonl"
	path, found, err := nativeconfig.FindFile(ctx, filepath.Join(configDir, "sessions"), func(name string) bool {
		return strings.HasSuffix(strings.ToLower(name), suffix)
	})
	if err != nil {
		return "", false, fmt.Errorf("pi: scan transcripts: %w", err)
	}
	return path, found, nil
}

func canonicalPiNativeSessionID(value string) (string, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", fmt.Errorf("pi: invalid native session id: %w", err)
	}
	return parsed.String(), nil
}
