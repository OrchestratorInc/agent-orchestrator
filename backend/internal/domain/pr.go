package domain

import (
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
)

type PRFacts struct {
	URL                      string
	Number                   int
	Draft                    bool
	Merged                   bool
	Closed                   bool
	CI                       CIState
	Review                   ReviewDecision
	Mergeability             Mergeability
	ReviewComments           bool
	SourceBranch             string
	TargetBranch             string
	HeadSHA                  string
	UpdatedAt                time.Time
	StateChangedAt           time.Time
	CIChangedAt              time.Time
	LastReviewAt             time.Time
	ExternalApproved         bool
	ExternalChangesRequested bool
	ExternalComments         bool
}

type PullRequest struct {
	URL            string
	URLAlias       string
	SessionID      SessionID
	Number         int
	Draft          bool
	Merged         bool
	Closed         bool
	CI             CIState
	Review         ReviewDecision
	Mergeability   Mergeability
	UpdatedAt      time.Time
	StateChangedAt time.Time

	Provider   string
	Host       string
	Repo       string
	ProviderID string

	SourceBranch    string
	TargetBranch    string
	HeadSHA         string
	Title           string
	Additions       int
	Deletions       int
	ChangedFiles    int
	Author          string
	AuthorAvatarURL string
	BaseSHA         string
	MergeCommitSHA  string

	ProviderState            string
	ProviderMergeable        string
	ProviderMergeStateStatus string
	HTMLURL                  string

	CreatedAtProvider time.Time
	UpdatedAtProvider time.Time
	MergedAtProvider  time.Time
	ClosedAtProvider  time.Time

	MetadataHash string
	CIHash       string
	ReviewHash   string

	ObservedAt       time.Time
	CIObservedAt     time.Time
	ReviewObservedAt time.Time
	ReviewPartial    bool
	AutoInjectCI     bool

	Details *PullRequestDetails
}

const MaxPullRequestDescriptionBytes = 64 * 1024

type PullRequestLabel struct {
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

type PullRequestRequestedReviewer struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatarUrl,omitempty"`
	IsTeam    bool   `json:"isTeam,omitempty"`
}

type PullRequestCommit struct {
	SHA         string    `json:"sha"`
	Headline    string    `json:"headline"`
	Author      string    `json:"author,omitempty"`
	CommittedAt time.Time `json:"committedAt,omitempty"`
}

const MaxPullRequestConversationComments = 50

const MaxPullRequestCommentBodyBytes = 8 * 1024

type PullRequestConversationComment struct {
	ID        string    `json:"id"`
	Author    string    `json:"author"`
	IsBot     bool      `json:"isBot,omitempty"`
	Body      string    `json:"body"`
	URL       string    `json:"url,omitempty"`
	CreatedAt time.Time `json:"createdAt,omitempty"`
}

type PullRequestDetails struct {
	Description          string
	DescriptionTruncated bool
	Labels               []PullRequestLabel
	RequestedReviewers   []PullRequestRequestedReviewer
	Commits              []PullRequestCommit
	CommitCount          int
	Conversation         []PullRequestConversationComment
	ObservedAt           time.Time
}

type PullRequestCheck struct {
	Name       string
	CommitHash string
	Status     PRCheckStatus
	Conclusion string
	URL        string
	Details    string
	LogTail    string
	CreatedAt  time.Time
}

type PullRequestComment struct {
	ThreadID         string
	ReviewID         string
	ID               string
	Author           string
	File             string
	Line             int
	Body             string
	URL              string
	Resolved         bool
	IsBot            bool
	CreatedAt        time.Time
	AutoInjectReview bool
}

func IsActionableReviewComment(resolved, isBot bool, file string, line int) bool {
	if resolved {
		return false
	}
	return !isBot || (strings.TrimSpace(file) != "" && line > 0)
}

type PullRequestReviewThread struct {
	ThreadID     string
	Path         string
	Line         int
	Resolved     bool
	IsBot        bool
	SemanticHash string
	UpdatedAt    time.Time
}

type PullRequestReview struct {
	ID               string
	Author           string
	State            ReviewDecision
	URL              string
	Body             string
	IsBot            bool
	TargetSHA        string
	SubmittedAt      time.Time
	AutoInjectReview bool
}

type CIState = contract.CIState

const (
	CIUnknown = contract.CIUnknown
	CIPending = contract.CIPending
	CIPassing = contract.CIPassing
	CIFailing = contract.CIFailing
)

type ReviewDecision = contract.ReviewDecision

const (
	ReviewNone           = contract.ReviewNone
	ReviewApproved       = contract.ReviewApproved
	ReviewChangesRequest = contract.ReviewChangesRequest
	ReviewRequired       = contract.ReviewRequired
)

type Mergeability = contract.Mergeability

const (
	MergeUnknown     = contract.MergeUnknown
	MergeMergeable   = contract.MergeMergeable
	MergeConflicting = contract.MergeConflicting
	MergeBlocked     = contract.MergeBlocked
	MergeUnstable    = contract.MergeUnstable
)

type PRState = contract.PRState

const (
	PRStateDraft  = contract.PRStateDraft
	PRStateOpen   = contract.PRStateOpen
	PRStateMerged = contract.PRStateMerged
	PRStateClosed = contract.PRStateClosed
)

type PRCheckStatus = contract.PRCheckStatus

const (
	PRCheckUnknown    = contract.PRCheckUnknown
	PRCheckQueued     = contract.PRCheckQueued
	PRCheckInProgress = contract.PRCheckInProgress
	PRCheckPassed     = contract.PRCheckPassed
	PRCheckFailed     = contract.PRCheckFailed
	PRCheckSkipped    = contract.PRCheckSkipped
	PRCheckCancelled  = contract.PRCheckCancelled
)

type MergeReadiness struct {
	Draft              bool
	Merged             bool
	Closed             bool
	CI                 CIState
	Review             ReviewDecision
	Mergeability       Mergeability
	UnresolvedComments bool
}

func (r MergeReadiness) ReadyToMerge() bool {
	if r.Merged || r.Closed || r.Draft {
		return false
	}
	ci := r.CI
	if ci == "" {
		ci = CIUnknown
	}
	switch ci {
	case CIFailing, CIPending, CIUnknown:
		return false
	}
	if r.Review == ReviewChangesRequest || r.UnresolvedComments {
		return false
	}
	return r.Mergeability == MergeMergeable
}

func MergeReadinessOf(pr PullRequest, hasUnresolvedComments bool) MergeReadiness {
	return MergeReadiness{
		Draft:              pr.Draft,
		Merged:             pr.Merged,
		Closed:             pr.Closed,
		CI:                 pr.CI,
		Review:             pr.Review,
		Mergeability:       pr.Mergeability,
		UnresolvedComments: hasUnresolvedComments,
	}
}
