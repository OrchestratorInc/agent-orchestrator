package pi

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
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
	sessionsDir := filepath.Join(configDir, "sessions")
	suffix := "_" + sessionID + ".jsonl"
	var found string
	err = filepath.WalkDir(sessionsDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// One unreadable folder elsewhere in the tree does not hide this
			// conversation, as the importer skips it too.
			if path != sessionsDir && errors.Is(walkErr, fs.ErrPermission) {
				if entry != nil && entry.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type().IsRegular() && strings.HasSuffix(strings.ToLower(entry.Name()), suffix) {
			found = path
			return fs.SkipAll
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("pi: scan transcripts: %w", err)
	}
	return found, found != "", nil
}

func canonicalPiNativeSessionID(value string) (string, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil {
		return "", fmt.Errorf("pi: invalid native session id: %w", err)
	}
	return parsed.String(), nil
}
