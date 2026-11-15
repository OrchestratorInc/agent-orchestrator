package ports

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type Tracker interface {
	Get(ctx context.Context, id domain.TrackerID) (domain.Issue, error)
	List(ctx context.Context, repo domain.TrackerRepo, filter domain.ListFilter) ([]domain.Issue, error)
	Preflight(ctx context.Context) error
}
