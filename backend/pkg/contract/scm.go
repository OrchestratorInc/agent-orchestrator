package contract

import "time"

type PRState string

const (
	PRStateDraft  PRState = "draft"
	PRStateOpen   PRState = "open"
	PRStateMerged PRState = "merged"
	PRStateClosed PRState = "closed"
)

type PRCheckStatus string

const (
	PRCheckUnknown    PRCheckStatus = "unknown"
	PRCheckQueued     PRCheckStatus = "queued"
	PRCheckInProgress PRCheckStatus = "in_progress"
	PRCheckPassed     PRCheckStatus = "passed"
	PRCheckFailed     PRCheckStatus = "failed"
	PRCheckSkipped    PRCheckStatus = "skipped"
	PRCheckCancelled  PRCheckStatus = "cancelled"
)

type AOReviewRunStatus string

const (
	AOReviewRunRunning   AOReviewRunStatus = "running"
	AOReviewRunComplete  AOReviewRunStatus = "complete"
	AOReviewRunDelivered AOReviewRunStatus = "delivered"
	AOReviewRunFailed    AOReviewRunStatus = "failed"
	AOReviewRunCancelled AOReviewRunStatus = "cancelled"
)

type AOReviewVerdict string

const (
	AOReviewVerdictNone             AOReviewVerdict = ""
	AOReviewVerdictApproved         AOReviewVerdict = "approved"
	AOReviewVerdictChangesRequested AOReviewVerdict = "changes_requested"
)

func (v AOReviewVerdict) Valid() bool {
	return v == AOReviewVerdictApproved || v == AOReviewVerdictChangesRequested
}

type AOReviewState string

const (
	AOReviewNeedsReview      AOReviewState = "needs_review"
	AOReviewRunning          AOReviewState = "running"
	AOReviewUpToDate         AOReviewState = "up_to_date"
	AOReviewChangesRequested AOReviewState = "changes_requested"
	AOReviewIneligible       AOReviewState = "ineligible"
)

type PullRequestFailingCheck struct {
	Name       string        `json:"name"`
	Status     PRCheckStatus `json:"status"`
	Conclusion string        `json:"conclusion"`
	URL        string        `json:"url,omitempty"`
}

type PullRequestCheckEntry struct {
	Name   string        `json:"name"`
	Status PRCheckStatus `json:"status"`
	URL    string        `json:"url,omitempty"`
}

type PullRequestCISummary struct {
	State         CIState                   `json:"state"`
	FailingChecks []PullRequestFailingCheck `json:"failingChecks"`
	Checks        []PullRequestCheckEntry   `json:"checks"`
	AutoInjectCI  bool                      `json:"autoInjectCI"`
}

const MaxPullRequestChecks = 200

type PullRequestReviewCommentLink struct {
	URL              string `json:"url,omitempty"`
	ReviewID         string `json:"reviewId,omitempty"`
	File             string `json:"file,omitempty"`
	Line             int    `json:"line,omitempty"`
	Body             string `json:"body,omitempty"`
	AutoInjectReview bool   `json:"autoInjectReview"`
}

type PullRequestUnresolvedReviewer struct {
	ReviewerID string                         `json:"reviewerId"`
	Count      int                            `json:"count"`
	Links      []PullRequestReviewCommentLink `json:"links"`
	ReviewURL  string                         `json:"reviewUrl,omitempty"`
	IsBot      bool                           `json:"isBot,omitempty"`
}

type PullRequestSubmittedReview struct {
	Reviewer         string         `json:"reviewerId"`
	Verdict          ReviewDecision `json:"verdict"`
	Body             string         `json:"body,omitempty"`
	URL              string         `json:"reviewUrl,omitempty"`
	SubmittedAt      time.Time      `json:"submittedAt"`
	IsBot            bool           `json:"isBot,omitempty"`
	AutoInjectReview bool           `json:"autoInjectReview"`
}

type PullRequestReviewSummary struct {
	Decision                   ReviewDecision                  `json:"decision"`
	HasUnresolvedHumanComments bool                            `json:"hasUnresolvedHumanComments"`
	UnresolvedThreadCount      *int                            `json:"unresolvedThreadCount,omitempty"`
	UnresolvedBy               []PullRequestUnresolvedReviewer `json:"unresolvedBy"`
	ResolvedBy                 []PullRequestUnresolvedReviewer `json:"resolvedBy,omitempty"`
	Reviews                    []PullRequestSubmittedReview    `json:"reviews"`
}

type PullRequestConflictFile struct {
	Path string `json:"path"`
	URL  string `json:"url,omitempty"`
}

type PullRequestMergeabilitySummary struct {
	State         Mergeability              `json:"state"`
	Reasons       []string                  `json:"reasons"`
	PRURL         string                    `json:"pullRequestUrl"`
	ConflictFiles []PullRequestConflictFile `json:"conflictFiles"`
}

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

type PullRequestSummary struct {
	URL              string                         `json:"url"`
	HTMLURL          string                         `json:"htmlUrl,omitempty"`
	Number           int                            `json:"number"`
	Title            string                         `json:"title"`
	State            PRState                        `json:"state"`
	Provider         string                         `json:"provider"`
	Repo             string                         `json:"repository"`
	Author           string                         `json:"author"`
	AuthorAvatarURL  string                         `json:"authorAvatarUrl,omitempty"`
	SourceBranch     string                         `json:"sourceBranch"`
	TargetBranch     string                         `json:"targetBranch"`
	HeadSHA          string                         `json:"headSha"`
	Additions        int                            `json:"additions"`
	Deletions        int                            `json:"deletions"`
	ChangedFiles     int                            `json:"changedFiles"`
	CI               PullRequestCISummary           `json:"ci"`
	Review           PullRequestReviewSummary       `json:"review"`
	Mergeability     PullRequestMergeabilitySummary `json:"mergeability"`
	StateChangedAt   time.Time                      `json:"stateChangedAt,omitempty"`
	CreatedAt        time.Time                      `json:"createdAt,omitempty"`
	UpdatedAt        time.Time                      `json:"updatedAt"`
	ObservedAt       time.Time                      `json:"observedAt"`
	CIObservedAt     time.Time                      `json:"ciObservedAt"`
	ReviewObservedAt time.Time                      `json:"reviewObservedAt"`

	Description          string                         `json:"description,omitempty"`
	DescriptionTruncated bool                           `json:"descriptionTruncated,omitempty"`
	Labels               []PullRequestLabel             `json:"labels"`
	RequestedReviewers   []PullRequestRequestedReviewer `json:"requestedReviewers"`
	Commits              []PullRequestCommit            `json:"commits"`
	CommitCount          int                            `json:"commitCount"`
	DetailsObservedAt    time.Time                      `json:"detailsObservedAt,omitempty"`

	Threads      []PullRequestThread       `json:"threads,omitempty"`
	Conversation []PullRequestCommentEntry `json:"conversation,omitempty"`
}

type PullRequestThread struct {
	ID       string                    `json:"id"`
	Path     string                    `json:"path,omitempty"`
	Line     int                       `json:"line,omitempty"`
	Resolved bool                      `json:"resolved"`
	Comments []PullRequestCommentEntry `json:"comments"`
}

type PullRequestCommentEntry struct {
	ID        string    `json:"id"`
	Author    string    `json:"author"`
	IsBot     bool      `json:"isBot"`
	Body      string    `json:"body"`
	URL       string    `json:"url,omitempty"`
	CreatedAt time.Time `json:"createdAt,omitempty"`
}

type AOReviewRun struct {
	ID               string            `json:"id"`
	ReviewID         string            `json:"reviewId"`
	SessionID        string            `json:"sessionId"`
	BatchID          string            `json:"batchId"`
	Harness          string            `json:"harness"`
	PRURL            string            `json:"pullRequestUrl"`
	TargetSHA        string            `json:"targetSha"`
	Status           AOReviewRunStatus `json:"status"`
	Verdict          AOReviewVerdict   `json:"verdict"`
	Body             string            `json:"body"`
	ProviderReviewID string            `json:"providerReviewId"`
	CreatedAt        time.Time         `json:"createdAt"`
	DeliveredAt      *time.Time        `json:"deliveredAt,omitempty"`
	AutoInjectReview bool              `json:"autoInjectReview"`
}

type AOPullRequestReviewState struct {
	PRURL          string        `json:"pullRequestUrl"`
	PRNumber       int           `json:"pullRequestNumber"`
	Title          string        `json:"title"`
	TargetSHA      string        `json:"targetSha"`
	Status         AOReviewState `json:"status"`
	StaleTargetSHA string        `json:"staleTargetSha,omitempty"`
	LatestRun      *AOReviewRun  `json:"latestRun,omitempty"`
	PreviousRun    *AOReviewRun  `json:"previousRun,omitempty"`
}
