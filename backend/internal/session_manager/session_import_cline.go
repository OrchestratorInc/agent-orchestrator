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

// The manifest is Cline's per-session process record. It survives exit, so a
// saved pid alone is not ownership: require live provider process evidence and
// reject pids started after the recorded session start.
func clineImportOwnedElsewhere(ctx context.Context, rec domain.SessionRecord) bool {
	source := rec.Metadata.ImportSource
	if source == nil || ctx.Err() != nil || !filepath.IsLocal(source.NativeID) || filepath.Base(source.NativeID) != source.NativeID {
		return false
	}
	root, err := os.OpenRoot(source.ConfigDir)
	if err != nil {
		return false
	}
	defer func() { _ = root.Close() }()
	f, err := root.Open(filepath.Join("sessions", source.NativeID, source.NativeID+".json"))
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	var live struct {
		ID      string    `json:"session_id"`
		PID     int       `json:"pid"`
		CWD     string    `json:"cwd"`
		Status  string    `json:"status"`
		Started time.Time `json:"started_at"`
	}
	err = json.NewDecoder(io.LimitReader(f, 4<<20)).Decode(&live)
	if err != nil || live.ID != source.NativeID || live.PID <= 0 || live.Status != "running" && live.Status != "idle" && live.Status != "pending" {
		return writableTranscriptOwner(ctx, rec.Metadata.NativeTranscriptPath)
	}
	if rec.Metadata.WorkspacePath != "" && pathWithin(rec.Metadata.WorkspacePath, live.CWD) {
		return false
	}
	if !processalive.Alive(live.PID) {
		return false
	}
	name, started, known := processStart(ctx, live.PID)
	name = strings.ToLower(name)
	return known && (strings.Contains(name, "cline") || strings.Contains(name, "bun") || strings.Contains(name, "node")) && (live.Started.IsZero() || !started.After(live.Started))
}
