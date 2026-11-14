package daemon

import (
	"context"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/previewserver"
)

type previewExitSessions interface {
	Get(ctx context.Context, id domain.SessionID) (domain.Session, error)
	SetPreview(ctx context.Context, id domain.SessionID, previewURL string) (domain.Session, error)
}

func wireManagedPreviewExit(managedPreview *previewserver.Manager, sessions previewExitSessions, log *slog.Logger) {
	managedPreview.SetOnExit(managedPreviewExitFunc(sessions, log))
}

func managedPreviewExitFunc(sessions previewExitSessions, log *slog.Logger) previewserver.OnExitFunc {
	return func(ctx context.Context, id domain.SessionID, status previewserver.Status) {
		sess, err := sessions.Get(ctx, id)
		if err != nil {
			log.Warn("fetch session to clear preview URL after managed preview crash", "session", id, "err", err)
			return
		}
		if status.URL == "" || sess.Metadata.PreviewURL != status.URL {
			return
		}
		if _, err := sessions.SetPreview(ctx, id, ""); err != nil {
			log.Warn("clear preview URL after managed preview crash", "session", id, "err", err)
		}
	}
}
