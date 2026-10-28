package domain

import "time"

// MergeRequest is a guest's request that a shared project's owner merge a
// pull request the guest cannot merge themselves.
type MergeRequest struct {
	ID            string
	OrgID         string
	ProjectID     string
	SessionID     string
	SessionName   string
	PullRequestID string
	PRNumber      int
	PRURL         string
	PRTitle       string
	Repository    string
	// RequestedBy is the guest who asked.
	RequestedByUserID string
	RequestedByName   string
	// Status is "pending", "merged" or "rejected".
	Status          string
	DecidedByUserID string
	DecidedByName   string
	DecidedAt       *time.Time
	CreatedAt       time.Time
}
