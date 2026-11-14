package pr

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

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

type MergeRequest struct {
	PRID            string
	PRURL           string
	ExpectedHeadSHA string
	Method          string
	AllowNoChecks   bool
}

type MergeResult struct {
	PRNumber       int
	Method         string
	State          domain.PRState
	MergeCommitSHA string
	StateChangedAt time.Time
}

type ResolveResult struct {
	Resolved int
}

type StateRequest struct {
	PRID   string
	PRURL  string
	Action string
}

type StateResult struct {
	PRNumber       int
	State          domain.PRState
	StateChangedAt time.Time
}

type CommentRequest struct {
	PRID        string
	PRURL       string
	Body        string
	ThreadID    string
	Attachments []CommentAttachment
}

type CommentAttachment struct {
	Name        string
	ContentType string
	Data        []byte
}

type AttachmentAccessResult struct {
	Allowed  bool
	Location string
	Reason   string
}

type CommentResult struct {
	ID        string
	URL       string
	CreatedAt time.Time
}
