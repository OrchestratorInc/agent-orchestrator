package daemon

import (
	"context"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const reportedPRTrackTimeout = 45 * time.Second

type reportedPRClaimer interface {
	TrackReportedPR(context.Context, domain.SessionID, string) (bool, error)
}

// reportPRTracker tracks a worker-reported PR off the report request: the
// claim fetches the PR from the provider, and `ao report` must not wait on it.
// A failed claim leaves the PR visible as a reported reference.
type reportPRTracker struct {
	sessions reportedPRClaimer
	log      *slog.Logger
}

func (t reportPRTracker) TrackReportedPR(ctx context.Context, id domain.SessionID, url string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), reportedPRTrackTimeout)
		defer cancel()
		claimed, err := t.sessions.TrackReportedPR(ctx, id, url)
		switch {
		case err != nil:
			t.log.Info("report: reported PR left as a reference", "session", id, "pr", url, "err", err)
		case claimed:
			t.log.Info("report: tracking reported PR outside the project repositories", "session", id, "pr", url)
		}
	}()
}
