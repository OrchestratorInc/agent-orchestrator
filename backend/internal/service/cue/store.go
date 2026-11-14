package cue

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type Store interface {
	InsertCue(ctx context.Context, cue domain.Cue) error
	SelectCueByID(ctx context.Context, cueID domain.CueID) (domain.Cue, bool, error)
	SelectCuesByProject(ctx context.Context, projectID domain.ProjectID) ([]domain.Cue, error)
	UpdateCue(ctx context.Context, cue domain.Cue) (domain.Cue, bool, error)
	DeleteCueByID(ctx context.Context, cueID domain.CueID) (bool, error)
}
