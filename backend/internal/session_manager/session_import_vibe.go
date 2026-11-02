package sessionmanager

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

var vibeImportID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

func vibeImportOwnedElsewhere(ctx context.Context, rec domain.SessionRecord) bool {
	if ctx.Err() != nil || rec.Metadata.ImportSource == nil {
		return false
	}
	source := rec.Metadata.ImportSource
	id := source.NativeID
	if len(rec.Metadata.ProviderConversationID) > 0 {
		id = rec.Metadata.ProviderConversationID
	}
	if !vibeImportID.MatchString(id) {
		return false
	}
	root, err := os.OpenRoot(source.ConfigDir)
	if err == nil {
		defer func() { _ = root.Close() }()
		file, err := root.Open(filepath.Join("logs", "session", "active", id+".lock.json"))
		if err == nil {
			var lease struct {
				ID  string `json:"session_id"`
				PID int    `json:"process_id"`
			}
			err = json.NewDecoder(io.LimitReader(file, 4096)).Decode(&lease)
			_ = file.Close()
			if err == nil && lease.ID == id && lease.PID > 0 && processalive.Alive(lease.PID) {
				return true
			}
		}
	}
	// Older Vibe releases do not publish leases. An open transcript is positive
	// evidence, but an append-open-close writer can escape this best-effort probe.
	return writableTranscriptOwner(ctx, rec.Metadata.NativeTranscriptPath)
}
