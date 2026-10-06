package ports

import (
	"context"
	"errors"
)

// ErrSCMHeadChanged indicates that the pull request advanced beyond the head
// SHA supplied with the mutation.
var ErrSCMHeadChanged = errors.New("scm: pull request head changed")

// ErrSCMNotMergeable indicates that the provider rejected the pull request's
// current mergeability state.
var ErrSCMNotMergeable = errors.New("scm: pull request not mergeable")

// ErrSCMUnsupported indicates that the configured SCM provider does not support
// the requested mutation.
var ErrSCMUnsupported = errors.New("scm: unsupported operation")

// SCMMergeMethod identifies the provider merge strategy.
type SCMMergeMethod string

// SCMMergeSquash requests a squash merge from the provider.
const SCMMergeSquash SCMMergeMethod = "squash"

// SCMMergeRequest uses ExpectedHeadSHA as a compare-and-swap guard. Provider
// implementations must reject the mutation if the live head has advanced.
type SCMMergeRequest struct {
	PR              SCMPRRef
	ExpectedHeadSHA string
	Method          SCMMergeMethod
}

// SCMMergeResult reports the provider's resulting merge commit.
type SCMMergeResult struct {
	MergeCommitSHA string
}

// SCMMerger mutates pull requests through an SCM provider.
type SCMMerger interface {
	MergePullRequest(ctx context.Context, request SCMMergeRequest) (SCMMergeResult, error)
}

// SCMReviewRequest asks the provider to request another review from Reviewer.
type SCMReviewRequest struct {
	PR       SCMPRRef
	Reviewer string
}

// SCMReviewRequester mutates pull-request review requests through an SCM provider.
type SCMReviewRequester interface {
	RequestReview(ctx context.Context, request SCMReviewRequest) error
}

// SCMReviewResolveRequest asks the provider to resolve one review thread.
type SCMReviewResolveRequest struct {
	PR       SCMPRRef
	ThreadID string
}

// SCMReviewResolver resolves pull-request review threads through an SCM provider.
type SCMReviewResolver interface {
	ResolveReviewThread(ctx context.Context, request SCMReviewResolveRequest) error
}

// SCMReviewSummaryRequest asks the provider to post one plain review comment
// on a pull request: a body, no inline comments, and no approval state.
type SCMReviewSummaryRequest struct {
	PR SCMPRRef
	// CommitSHA pins the review to the commit that was reviewed.
	CommitSHA string
	Body      string
}

// SCMReviewSummaryPoster posts AO's own review summaries. It never opens review
// threads: AO's findings live in AO, so nothing posted here becomes a comment
// the review-comment watcher would forward back to the worker.
type SCMReviewSummaryPoster interface {
	PostReviewSummary(ctx context.Context, request SCMReviewSummaryRequest) (reviewID string, err error)
}
