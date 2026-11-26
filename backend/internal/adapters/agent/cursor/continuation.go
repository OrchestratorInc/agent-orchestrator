package cursor

import (
	"context"
	"errors"
	"path/filepath"

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

// PrepareImportedSession copies the chat to where the resume looks.
// Cursor's direct --resume path uses process.cwd() for this lookup and creates
// tools from the launch workspace, independently of the saved conversation graph.
// ACP's session/load reads only acp-sessions/<id>, so a Chat import is copied
// there under its own id (see Layout.SessionID); the terminal CLI's own chat
// folder never holds the copy.
func (*Plugin) PrepareImportedSession(ctx context.Context, r ports.ImportedSessionRelocation) (ports.NativeSessionRef, error) {
	layout := cursorstore.LayoutTerminal
	if r.Chat {
		layout = cursorstore.LayoutACP
	}
	if path, found, err := cursorstore.PreparedCopyIn(ctx, layout, r.ConfigDir, r.TranscriptPath, r.LaunchCWD, r.NativeSessionID, r.SourceFingerprint); err != nil {
		return ports.NativeSessionRef{}, err
	} else if found {
		return ports.NativeSessionRef{NativeSessionID: layout.SessionID(r.NativeSessionID, r.LaunchCWD, r.SourceFingerprint), ConfigDir: r.ConfigDir, TranscriptPath: path}, nil
	}
	archive, err := cursorstore.Read(ctx, r.ConfigDir, r.TranscriptPath)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	if archive.ID != r.NativeSessionID {
		return ports.NativeSessionRef{}, errors.New("cursor source no longer holds the imported conversation")
	}
	if r.SourceCWD != "" && archive.CWD != r.SourceCWD {
		return ports.NativeSessionRef{}, errors.New("cursor source workspace changed since selection")
	}
	path, err := cursorstore.CopySelectedIn(ctx, layout, r.ConfigDir, r.TranscriptPath, r.LaunchCWD, r.SourceFingerprint)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	return ports.NativeSessionRef{NativeSessionID: layout.SessionID(archive.ID, r.LaunchCWD, r.SourceFingerprint), ConfigDir: r.ConfigDir, TranscriptPath: path}, nil
}

// PrepareRuntimeLaunch verifies the exact imported cwd bucket before Cursor
// can initialize a missing SQLite store and silently continue an empty chat.
// Ordinary managed launches keep their existing behavior.
func (p *Plugin) PrepareRuntimeLaunch(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if cfg.ImportConfigDir == "" {
		return nil
	}
	if cfg.NativeSessionID == "" {
		return errors.New("cursor imported restore is missing its exact native binding")
	}
	root, err := p.NativeSessionConfigDir(ctx, cfg.Env)
	if err != nil {
		return err
	}
	selected, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	recorded, err := filepath.EvalSymlinks(cfg.ImportConfigDir)
	if err != nil {
		return err
	}
	if selected != recorded {
		return errors.New("cursor launch profile differs from the imported native root")
	}
	path, err := cursorstore.SessionPath(root, cfg.WorkspacePath, cfg.NativeSessionID)
	if err != nil {
		return err
	}
	return cursorstore.Verify(ctx, root, path, cfg.NativeSessionID)
}
