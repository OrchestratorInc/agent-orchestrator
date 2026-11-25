package crush

import (
	"context"
	"path/filepath"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

var _ ports.AgentImportedSessionPreparer = (*Plugin)(nil)

// PrepareImportedSession prepares the selected native store before AO checkpoints
// it. Later launch retries reuse that checkpoint instead of relocating again.
func (p *Plugin) PrepareImportedSession(ctx context.Context, r ports.ImportedSessionRelocation) (ports.NativeSessionRef, error) {
	prepared, err := preparedCrushCopy(ctx, r)
	if err != nil {
		return ports.NativeSessionRef{}, err
	}
	if !prepared {
		if err := p.RelocateImportedSession(ctx, r); err != nil {
			return ports.NativeSessionRef{}, err
		}
	}
	return ports.NativeSessionRef{
		NativeSessionID: r.NativeSessionID,
		ConfigDir:       r.ConfigDir,
		TranscriptPath:  filepath.Join(r.LaunchCWD, ".crush", "crush.db", r.NativeSessionID),
	}, nil
}
