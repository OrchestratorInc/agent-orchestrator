package ports

import (
	"context"
	"errors"
	"time"
)

var ErrSCMHeadChanged = errors.New("scm: pull request head changed")

var ErrSCMNotMergeable = errors.New("scm: pull request not mergeable")

var ErrSCMRejected = errors.New("scm: provider rejected mutation")

var ErrSCMUnsupported = errors.New("scm: unsupported operation")

var ErrSCMForbidden = errors.New("scm: forbidden")

type SCMMergeMethod string

const (
	SCMMergeSquash SCMMergeMethod = "squash"
	SCMMergeMerge  SCMMergeMethod = "merge"
	SCMMergeRebase SCMMergeMethod = "rebase"
)

func (m SCMMergeMethod) Valid() bool {
	switch m {
	case SCMMergeSquash, SCMMergeMerge, SCMMergeRebase:
		return true
	default:
		return false
	}
}

type SCMMergeRequest struct {
	PR              SCMPRRef
	ExpectedHeadSHA string
	Method          SCMMergeMethod
}

type SCMMergeResult struct {
	MergeCommitSHA string
}

type SCMMerger interface {
	MergePullRequest(ctx context.Context, request SCMMergeRequest) (SCMMergeResult, error)
}

type SCMReviewRequest struct {
	PR       SCMPRRef
	Reviewer string
}

type SCMReviewRequester interface {
	RequestReview(ctx context.Context, request SCMReviewRequest) error
}

type SCMReviewResolveRequest struct {
	PR       SCMPRRef
	ThreadID string
}

type SCMReviewResolver interface {
	ResolveReviewThread(ctx context.Context, request SCMReviewResolveRequest) error
}

type SCMPRStateAction string

const (
	SCMPRClose  SCMPRStateAction = "close"
	SCMPRReopen SCMPRStateAction = "reopen"
	SCMPRReady  SCMPRStateAction = "ready"
	SCMPRDraft  SCMPRStateAction = "draft"
)

func (a SCMPRStateAction) Valid() bool {
	switch a {
	case SCMPRClose, SCMPRReopen, SCMPRReady, SCMPRDraft:
		return true
	default:
		return false
	}
}

type SCMPRStateRequest struct {
	PR     SCMPRRef
	Action SCMPRStateAction
	NodeID string
}

type SCMPRStateChanger interface {
	ChangePullRequestState(ctx context.Context, request SCMPRStateRequest) error
}

type SCMCommentRequest struct {
	PR       SCMPRRef
	Body     string
	ThreadID string
}

type SCMCommentResult struct {
	ID        string
	URL       string
	Author    string
	IsBot     bool
	Body      string
	CreatedAt time.Time
}

type SCMCommenter interface {
	PostComment(ctx context.Context, request SCMCommentRequest) (SCMCommentResult, error)
}

type SCMAttachmentFile struct {
	Name        string
	ContentType string
	Data        []byte
}

type SCMAttachmentBatch struct {
	PR    SCMPRRef
	Files []SCMAttachmentFile
}

type SCMStoredAttachments struct {
	URLs   []string
	Handle string
}

type SCMAttachmentAccess struct {
	Allowed  bool
	Location string
	Reason   string
}

const SCMAttachmentNoWriteAccess = "no_write_access"

type SCMAttachmentStore interface {
	AttachmentAccess(ctx context.Context, pr SCMPRRef) (SCMAttachmentAccess, error)
	StoreAttachments(ctx context.Context, batch SCMAttachmentBatch) (SCMStoredAttachments, error)
	DiscardAttachments(ctx context.Context, pr SCMPRRef, handle string) error
}
