package cursor

import (
	"context"
	"errors"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/cursorstore"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/nativeconfig"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var (
	_ ports.AgentNativeSessionConfigProvider = (*Plugin)(nil)
	_ ports.AgentNativeSessionProber         = (*Plugin)(nil)
	_ ports.AgentTranscriptLocator           = (*Plugin)(nil)
	_ ports.AgentImportedSessionPreparer     = (*Plugin)(nil)
)

// NativeSessionConfigDir resolves the native Cursor profile without copying auth.
func (*Plugin) NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return nativeconfig.Resolve(env, "CURSOR_DATA_DIR", ".cursor")
}

// LocateTranscript finds a complete native graph with the requested identity.
func (*Plugin) LocateTranscript(ctx context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	if ref.ConfigDir == "" || ref.NativeSessionID == "" {
		return "", false, nil
	}
	if ref.TranscriptPath != "" {
		archive, err := cursorstore.Read(ctx, ref.ConfigDir, ref.TranscriptPath)
		if err != nil {
			return "", false, err
		}
		return ref.TranscriptPath, archive.ID == ref.NativeSessionID, nil
	}
	return cursorstore.Find(ctx, ref.ConfigDir, ref.NativeSessionID)
}

// ProbeNativeSession refuses missing or incomplete native state.
func (p *Plugin) ProbeNativeSession(ctx context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	if ref.ConfigDir == "" {
		return ports.NativeSessionAvailabilityUnknown, nil
	}
	_, found, err := p.LocateTranscript(ctx, ref)
	if err != nil {
		return ports.NativeSessionAvailabilityUnknown, err
	}
	if found {
		return ports.NativeSessionAvailabilityAvailable, nil
	}
	return ports.NativeSessionAvailabilityUnavailable, nil
}

// PrepareImportedSession copies the same native id to the launch cwd's bucket.
// Cursor's direct --resume path uses process.cwd() for this lookup and creates
// tools from the launch workspace, independently of the saved conversation graph.
func (*Plugin) PrepareImportedSession(ctx context.Context, r ports.ImportedSessionRelocation) (ports.NativeSessionRef, error) {
	archive, err := cursorstore.Read(ctx, r.ConfigDir, r.TranscriptPath)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	if archive.ID != r.NativeSessionID {
		return ports.NativeSessionRef{}, errors.New("cursor source no longer holds the imported conversation")
	}
	path, err := cursorstore.Copy(ctx, r.ConfigDir, r.TranscriptPath, r.LaunchCWD)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	return ports.NativeSessionRef{NativeSessionID: archive.ID, ConfigDir: r.ConfigDir, TranscriptPath: path}, nil
}
