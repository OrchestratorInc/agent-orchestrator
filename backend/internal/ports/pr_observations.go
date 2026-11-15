package ports

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var ErrSCMPRNotFound = ErrSCMNotFound

type PRObserver interface {
	Observe(ctx context.Context, prURL string) (PRObservation, error)
}

type PRObservation struct {
	Fetched      bool
	URL          string
	Number       int
	Title        string
	SourceBranch string
	TargetBranch string
	Draft        bool
	Merged       bool
	Closed       bool
	CI           domain.CIState
	Review       domain.ReviewDecision
	Mergeability domain.Mergeability
	Checks       []PRCheckObservation
	Comments     []PRCommentObservation
}

type PRCheckObservation struct {
	Name       string
	CommitHash string
	Status     domain.PRCheckStatus
	URL        string
	LogTail    string
}

type PRCommentObservation struct {
	ID               string
	ThreadID         string
	ReviewID         string
	Author           string
	File             string
	Line             int
	Body             string
	URL              string
	Resolved         bool
	AutoInjectReview bool
}
