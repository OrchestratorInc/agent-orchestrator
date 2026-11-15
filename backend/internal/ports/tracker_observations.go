package ports

import (
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type TrackerObservation struct {
	Fetched    bool
	ObservedAt time.Time

	Provider string
	Host     string
	Repo     string

	Issue    TrackerIssueObservation
	Comments []TrackerCommentObservation

	Changed TrackerChanged
}

type TrackerChanged struct {
	State    bool
	Assignee bool
	Comments bool
}

type TrackerIssueObservation struct {
	URL      string
	Number   int
	State    domain.NormalizedIssueState
	Title    string
	Body     string
	Assignee string
	Author   string
	Labels   []string

	CreatedAtProvider time.Time
	UpdatedAtProvider time.Time
	ClosedAtProvider  time.Time
}

type TrackerCommentObservation struct {
	ID                string
	Author            string
	Body              string
	URL               string
	IsBot             bool
	CreatedAtProvider time.Time
	UpdatedAtProvider time.Time
}
