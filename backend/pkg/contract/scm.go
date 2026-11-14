package contract

import "time"

// PRState is the normalized lifecycle of a pull request.
type PRState string

// Pull-request lifecycle states.
const (
	PRStateDraft  PRState = "draft"
	PRStateOpen   PRState = "open"
	PRStateMerged PRState = "merged"
	PRStateClosed PRState = "closed"
)

// PRCheckStatus is one CI check run's normalized status.
type PRCheckStatus string

// Pull-request check states.
const (
	PRCheckUnknown    PRCheckStatus = "unknown"
	PRCheckQueued     PRCheckStatus = "queued"
	PRCheckInProgress PRCheckStatus = "in_progress"
	PRCheckPassed     PRCheckStatus = "passed"
	PRCheckFailed     PRCheckStatus = "failed"
	PRCheckSkipped    PRCheckStatus = "skipped"
	PRCheckCancelled  PRCheckStatus = "cancelled"
)

// AOReviewRunStatus is the lifecycle state of one AO review pass.
type AOReviewRunStatus string

// AO review-run states.
const (
	AOReviewRunRunning   AOReviewRunStatus = "running"
	AOReviewRunComplete  AOReviewRunStatus = "complete"
	AOReviewRunDelivered AOReviewRunStatus = "delivered"
	AOReviewRunFailed    AOReviewRunStatus = "failed"
	AOReviewRunCancelled AOReviewRunStatus = "cancelled"
)

// AOReviewVerdict is the outcome of an AO review pass. An empty verdict means
// the pass has not produced an outcome.
type AOReviewVerdict string

// AO review verdicts.
const (
	AOReviewVerdictNone             AOReviewVerdict = ""
	AOReviewVerdictApproved         AOReviewVerdict = "approved"
	AOReviewVerdictChangesRequested AOReviewVerdict = "changes_requested"
)

// Valid reports whether v is a verdict a reviewer may submit.
func (v AOReviewVerdict) Valid() bool {
	return v == AOReviewVerdictApproved || v == AOReviewVerdictChangesRequested
}

// AOReviewState is the current AO review state for one pull request head.
type AOReviewState string

// AO review states for the current pull-request head.
const (
	AOReviewNeedsReview      AOReviewState = "needs_review"
	AOReviewRunning          AOReviewState = "running"
	AOReviewUpToDate         AOReviewState = "up_to_date"
	AOReviewChangesRequested AOReviewState = "changes_requested"
	AOReviewIneligible       AOReviewState = "ineligible"
)

// PullRequestFailingCheck is one failed or cancelled CI check.
type PullRequestFailingCheck struct {
	Name       string        `json:"name"`
	Status     PRCheckStatus `json:"status"`
	Conclusion string        `json:"conclusion"`
	URL        string        `json:"url,omitempty"`
}

// PullRequestCheckEntry is one CI check on the PR's head commit.
type PullRequestCheckEntry struct {
	Name   string        `json:"name"`
	Status PRCheckStatus `json:"status"`
	URL    string        `json:"url,omitempty"`
}

// PullRequestCISummary is the latest aggregate CI observation.
type PullRequestCISummary struct {
	State         CIState                   `json:"state"`
	FailingChecks []PullRequestFailingCheck `json:"failingChecks"`
	// Checks lists every check on the head commit, failing first, so a view can
	// say "23/23 passed" and list them by name. Capped at MaxPullRequestChecks.
	Checks       []PullRequestCheckEntry `json:"checks"`
	AutoInjectCI bool                    `json:"autoInjectCI"`
}

// MaxPullRequestChecks caps PullRequestCISummary.Checks.
const MaxPullRequestChecks = 200

// PullRequestReviewCommentLink points to one review comment.
type PullRequestReviewCommentLink struct {
	URL              string `json:"url,omitempty"`
	ReviewID         string `json:"reviewId,omitempty"`
	File             string `json:"file,omitempty"`
	Line             int    `json:"line,omitempty"`
	Body             string `json:"body,omitempty"`
	AutoInjectReview bool   `json:"autoInjectReview"`
}

// PullRequestUnresolvedReviewer groups review comments by reviewer.
type PullRequestUnresolvedReviewer struct {
	ReviewerID string                         `json:"reviewerId"`
	Count      int                            `json:"count"`
	Links      []PullRequestReviewCommentLink `json:"links"`
	ReviewURL  string                         `json:"reviewUrl,omitempty"`
	IsBot      bool                           `json:"isBot,omitempty"`
}

// PullRequestSubmittedReview is one provider review summary.
type PullRequestSubmittedReview struct {
	Reviewer         string         `json:"reviewerId"`
	Verdict          ReviewDecision `json:"verdict"`
	Body             string         `json:"body,omitempty"`
	URL              string         `json:"reviewUrl,omitempty"`
	SubmittedAt      time.Time      `json:"submittedAt"`
	IsBot            bool           `json:"isBot,omitempty"`
	AutoInjectReview bool           `json:"autoInjectReview"`
}

// PullRequestReviewSummary is the latest aggregate provider review observation.
type PullRequestReviewSummary struct {
	Decision                   ReviewDecision `json:"decision"`
	HasUnresolvedHumanComments bool           `json:"hasUnresolvedHumanComments"`
	// UnresolvedThreadCount counts unresolved human review threads, rather than
	// individual comments (a thread can contain several comments). It is a
	// pointer so an unknown count (never observed, or observed only partially)
	// stays absent on the wire while an observed zero serializes explicitly.
	UnresolvedThreadCount *int                            `json:"unresolvedThreadCount,omitempty"`
	UnresolvedBy          []PullRequestUnresolvedReviewer `json:"unresolvedBy"`
	ResolvedBy            []PullRequestUnresolvedReviewer `json:"resolvedBy,omitempty"`
	Reviews               []PullRequestSubmittedReview    `json:"reviews"`
}

// PullRequestConflictFile is one file involved in a merge conflict.
type PullRequestConflictFile struct {
	Path string `json:"path"`
	URL  string `json:"url,omitempty"`
}

// PullRequestMergeabilitySummary reports whether a PR can merge and why.
type PullRequestMergeabilitySummary struct {
	State         Mergeability              `json:"state"`
	Reasons       []string                  `json:"reasons"`
	PRURL         string                    `json:"pullRequestUrl"`
	ConflictFiles []PullRequestConflictFile `json:"conflictFiles"`
}

// PullRequestLabel is one label on a pull request. Color is a provider hex
// string without the leading "#".
type PullRequestLabel struct {
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

// PullRequestRequestedReviewer is a user or team whose review is requested and
// not yet given.
type PullRequestRequestedReviewer struct {
	Login     string `json:"login"`
	AvatarURL string `json:"avatarUrl,omitempty"`
	IsTeam    bool   `json:"isTeam,omitempty"`
}

// PullRequestCommit is one commit on the pull request's branch.
type PullRequestCommit struct {
	SHA         string    `json:"sha"`
	Headline    string    `json:"headline"`
	Author      string    `json:"author,omitempty"`
	CommittedAt time.Time `json:"committedAt,omitempty"`
}

// PullRequestSummary is the normalized raw SCM read model for one pull request.
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

	// Description is the PR body as markdown. It is untrusted provider content.
	Description          string `json:"description,omitempty"`
	DescriptionTruncated bool   `json:"descriptionTruncated,omitempty"`
	// Labels, RequestedReviewers and Commits are empty until DetailsObservedAt
	// is set, and empty afterwards only when the PR has none.
	Labels             []PullRequestLabel             `json:"labels"`
	RequestedReviewers []PullRequestRequestedReviewer `json:"requestedReviewers"`
	// Commits are the newest commits, oldest first; CommitCount is the total.
	Commits     []PullRequestCommit `json:"commits"`
	CommitCount int                 `json:"commitCount"`
	// DetailsObservedAt is zero until an observation has carried the details.
	DetailsObservedAt time.Time `json:"detailsObservedAt,omitempty"`

	// Threads are the stored review threads with their comments, oldest first.
	Threads []PullRequestThread `json:"threads,omitempty"`
	// Conversation is the newest top-level conversation comments, oldest
	// first. It is empty until DetailsObservedAt is set.
	Conversation []PullRequestCommentEntry `json:"conversation,omitempty"`
}

// PullRequestThread is one review thread with its stored comments, oldest
// first.
type PullRequestThread struct {
	ID       string                    `json:"id"`
	Path     string                    `json:"path,omitempty"`
	Line     int                       `json:"line,omitempty"`
	Resolved bool                      `json:"resolved"`
	Comments []PullRequestCommentEntry `json:"comments"`
}

// PullRequestCommentEntry is one review-thread or conversation comment. Body
// is untrusted provider markdown.
type PullRequestCommentEntry struct {
	ID        string    `json:"id"`
	Author    string    `json:"author"`
	IsBot     bool      `json:"isBot"`
	Body      string    `json:"body"`
	URL       string    `json:"url,omitempty"`
	CreatedAt time.Time `json:"createdAt,omitempty"`
}

// AOReviewRun is one transport-neutral AO review pass.
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

// AOPullRequestReviewState is AO's current review state for one PR head.
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
