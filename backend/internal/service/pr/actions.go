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
	UploadAttachment(ctx context.Context, request AttachmentRequest) (AttachmentResult, error)
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
type CommentRequest struct {
	PRID     string
	PRURL    string
	Body     string
	ThreadID string
}

// CommentResult is the provider's record of a posted comment.
type CommentResult struct {
	ID        string
	URL       string
	CreatedAt time.Time
}

// AttachmentRequest uploads a file for a comment on the tracked PR. Data is
// the decoded file; ContentType is what the client declared for it.
type AttachmentRequest struct {
	PRID        string
	PRURL       string
	Name        string
	ContentType string
	Data        []byte
}

// AttachmentResult locates an uploaded attachment. Name is the sanitized file
// name and Markdown embeds (images) or links (other files) URL.
type AttachmentResult struct {
	URL      string
	Name     string
	Markdown string
}
