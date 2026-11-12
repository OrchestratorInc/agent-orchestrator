package pr

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ActionManager is the controller-facing contract for guarded PR mutations.
type ActionManager interface {
	Merge(ctx context.Context, request MergeRequest) (MergeResult, error)
	ResolveComments(ctx context.Context, prID string, commentIDs []string) (ResolveResult, error)
	ChangeState(ctx context.Context, request StateRequest) (StateResult, error)
	PostComment(ctx context.Context, request CommentRequest) (CommentResult, error)
	AttachmentAccess(ctx context.Context, prID, prURL string) (AttachmentAccessResult, error)
	Stack(ctx context.Context, prID, prURL string) (StackResult, error)
	AddToStack(ctx context.Context, prID, prURL string) (StackResult, error)
	MergeStack(ctx context.Context, request StackMergeRequest) (StackMergeResult, error)
	LoadDetails(ctx context.Context, prID, prURL string) error
}

// MergeRequest identifies the tracked PR and pins the mutation to the exact
// head the user saw when they clicked Merge.
type MergeRequest struct {
	PRID            string
	PRURL           string
	ExpectedHeadSHA string
	// Method is the provider merge strategy: squash (the default when empty),
	// merge, or rebase.
	Method string
	// AllowNoChecks lets a PR that nothing checks merge, once the provider
	// confirms nothing has started checking its head for a while. The PR view
	// asks for it; the CLI doesn't, so `ao pr merge` still waits for checks.
	AllowNoChecks bool
}

// MergeResult is the successful outcome of a PR merge.
type MergeResult struct {
	PRNumber       int
	Method         string
	State          domain.PRState
	MergeCommitSHA string
	StateChangedAt time.Time
}

// ResolveResult is the successful outcome of a resolve-comments operation.
type ResolveResult struct {
	Resolved int
}

// StateRequest asks to close, reopen, mark ready, or convert to draft the
// tracked PR identified by PRID and PRURL.
type StateRequest struct {
	PRID   string
	PRURL  string
	Action string
}

// StateResult is the successful outcome of a PR state change.
type StateResult struct {
	PRNumber       int
	State          domain.PRState
	StateChangedAt time.Time
}

// CommentRequest posts Body on the tracked PR. An empty ThreadID posts a
// top-level conversation comment; otherwise it replies in that review thread.
//
// Attachments are the comment's files. Body links each one by its index, as
// "](attachment:0)"; they are stored with the PR's SCM provider only when the
// comment is posted, and removed again if posting fails.
type CommentRequest struct {
	PRID        string
	PRURL       string
	Body        string
	ThreadID    string
	Attachments []CommentAttachment
}

// CommentAttachment is one file of a comment. Data is the decoded file;
// ContentType is what the client declared for it.
type CommentAttachment struct {
	Name        string
	ContentType string
	Data        []byte
}

// AttachmentAccessResult says whether the person can attach files to this
// PR's comments, and where they would be stored.
type AttachmentAccessResult struct {
	Allowed  bool
	Location string
	Reason   string
}

// CommentResult is the provider's record of a posted comment.
type CommentResult struct {
	ID        string
	URL       string
	CreatedAt time.Time
}
