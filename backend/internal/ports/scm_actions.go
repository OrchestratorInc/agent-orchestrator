package ports

import (
	"context"
	"errors"
	"time"
)

// ErrSCMHeadChanged indicates that the pull request advanced beyond the head
// SHA supplied with the mutation.
var ErrSCMHeadChanged = errors.New("scm: pull request head changed")

// ErrSCMNotMergeable indicates that the provider rejected the pull request's
// current mergeability state.
var ErrSCMNotMergeable = errors.New("scm: pull request not mergeable")

// ErrSCMRejected indicates that the provider refused a well-formed mutation,
// for example an invalid state transition or a validation failure.
var ErrSCMRejected = errors.New("scm: provider rejected mutation")

// ErrSCMUnsupported indicates that the configured SCM provider does not support
// the requested mutation.
var ErrSCMUnsupported = errors.New("scm: unsupported operation")

// ErrSCMForbidden reports that the signed-in account may not make a change,
// such as writing to a repository it can only read.
var ErrSCMForbidden = errors.New("scm: forbidden")

// SCMMergeMethod identifies the provider merge strategy.
type SCMMergeMethod string

// Provider merge strategies.
const (
	// SCMMergeSquash requests a squash merge from the provider.
	SCMMergeSquash SCMMergeMethod = "squash"
	// SCMMergeMerge requests a merge commit from the provider.
	SCMMergeMerge SCMMergeMethod = "merge"
	// SCMMergeRebase requests a rebase merge from the provider.
	SCMMergeRebase SCMMergeMethod = "rebase"
)

// Valid reports whether m is one of the known merge strategies.
func (m SCMMergeMethod) Valid() bool {
	switch m {
	case SCMMergeSquash, SCMMergeMerge, SCMMergeRebase:
		return true
	default:
		return false
	}
}

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

// SCMPRStateAction is a pull-request lifecycle mutation.
type SCMPRStateAction string

// Pull-request lifecycle mutations.
const (
	// SCMPRClose closes an open or draft pull request without merging it.
	SCMPRClose SCMPRStateAction = "close"
	// SCMPRReopen reopens a closed, unmerged pull request.
	SCMPRReopen SCMPRStateAction = "reopen"
	// SCMPRReady marks a draft pull request ready for review.
	SCMPRReady SCMPRStateAction = "ready"
	// SCMPRDraft converts an open pull request back to a draft.
	SCMPRDraft SCMPRStateAction = "draft"
)

// Valid reports whether a is one of the known state actions.
func (a SCMPRStateAction) Valid() bool {
	switch a {
	case SCMPRClose, SCMPRReopen, SCMPRReady, SCMPRDraft:
		return true
	default:
		return false
	}
}

// SCMPRStateRequest asks the provider to apply Action to PR. NodeID is the
// provider's opaque PR identifier when AO already knows it (GitHub's GraphQL
// node id); providers that need it look it up when it is empty.
type SCMPRStateRequest struct {
	PR     SCMPRRef
	Action SCMPRStateAction
	NodeID string
}

// SCMPRStateChanger closes, reopens, and toggles the draft state of pull
// requests through an SCM provider. Providers return ErrSCMUnsupported for an
// action they cannot express.
type SCMPRStateChanger interface {
	ChangePullRequestState(ctx context.Context, request SCMPRStateRequest) error
}

// SCMCommentRequest posts Body on PR. An empty ThreadID posts a top-level
// conversation comment; otherwise the comment replies in that review thread.
type SCMCommentRequest struct {
	PR       SCMPRRef
	Body     string
	ThreadID string
}

// SCMCommentResult is the provider's record of a posted comment.
type SCMCommentResult struct {
	ID        string
	URL       string
	Author    string
	IsBot     bool
	Body      string
	CreatedAt time.Time
}

// SCMCommenter posts pull-request comments through an SCM provider.
type SCMCommenter interface {
	PostComment(ctx context.Context, request SCMCommentRequest) (SCMCommentResult, error)
}

// SCMAttachmentFile is one file attached to a pull-request comment. Name is
// already a safe file name, ContentType the validated media type, and Data the
// decoded file bytes.
type SCMAttachmentFile struct {
	Name        string
	ContentType string
	Data        []byte
}

// SCMAttachmentBatch is the files of one comment on PR, stored together.
type SCMAttachmentBatch struct {
	PR    SCMPRRef
	Files []SCMAttachmentFile
}

// SCMStoredAttachments locates a stored batch: one absolute URL per file, in
// the batch's order, and the handle DiscardAttachments takes to remove the
// batch again. Handle is empty when the provider keeps nothing to remove.
type SCMStoredAttachments struct {
	URLs   []string
	Handle string
}

// SCMAttachmentAccess says whether the signed-in account can store comment
// attachments for a PR. Location names where they go, so a person can be told
// before the first upload; Reason says why not when Allowed is false.
type SCMAttachmentAccess struct {
	Allowed  bool
	Location string
	Reason   string
}

// SCMAttachmentNoWriteAccess is the Reason when the account cannot write
// where the provider stores attachments, such as a fork PR's base repository.
const SCMAttachmentNoWriteAccess = "no_write_access"

// SCMAttachmentStore stores the files a pull-request comment links to or
// embeds. A comment's files are stored when it is posted, not when they are
// picked, and are discarded again when posting fails.
type SCMAttachmentStore interface {
	AttachmentAccess(ctx context.Context, pr SCMPRRef) (SCMAttachmentAccess, error)
	StoreAttachments(ctx context.Context, batch SCMAttachmentBatch) (SCMStoredAttachments, error)
	DiscardAttachments(ctx context.Context, pr SCMPRRef, handle string) error
}
