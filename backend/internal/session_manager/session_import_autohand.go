package sessionmanager

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

// Autohand publishes active-agents/<session-id>[.<instance>].json. Older
// clients may not publish this registry; their append/close transcript writes
// are visible to the writable-file fallback only during an actual write.
func autohandImportOwnedElsewhere(ctx context.Context, rec domain.SessionRecord) bool {
	source := rec.Metadata.ImportSource
	if source == nil || ctx.Err() != nil {
		return false
	}
	root, err := os.OpenRoot(source.ConfigDir)
	if err != nil {
		return false
	}
	defer func() { _ = root.Close() }()
	dir, err := root.Open("active-agents")
	if err == nil {
		defer func() { _ = dir.Close() }()
		entries, _ := dir.ReadDir(-1)
		for _, entry := range entries {
			if ctx.Err() != nil {
				return false
			}
			if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			f, err := root.Open(filepath.Join("active-agents", entry.Name()))
			if err != nil {
				continue
			}
			var live struct {
				ID      string    `json:"sessionId"`
				PID     int       `json:"pid"`
				CWD     string    `json:"workspaceRoot"`
				Started time.Time `json:"startedAt"`
			}
			err = json.NewDecoder(io.LimitReader(f, 64<<10)).Decode(&live)
			_ = f.Close()
			if err != nil || live.ID != source.NativeID || live.PID <= 0 || !processalive.Alive(live.PID) {
				continue
			}
			if rec.Metadata.WorkspacePath != "" && pathWithin(rec.Metadata.WorkspacePath, live.CWD) {
				continue
			}
			name, started, known := processStart(ctx, live.PID)
			name = strings.ToLower(name)
			if known && (strings.Contains(name, "autohand") || strings.Contains(name, "bun") || strings.Contains(name, "node")) && (live.Started.IsZero() || !started.After(live.Started)) {
				return true
			}
		}
	}
	return writableTranscriptOwner(ctx, rec.Metadata.NativeTranscriptPath)
}
