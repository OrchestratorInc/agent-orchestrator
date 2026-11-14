package ports

import "context"

type SCMStackLayer struct {
	Number     int
	Open       bool
	Draft      bool
	Merged     bool
	HeadBranch string
	HeadSHA    string
}

type SCMStack struct {
	Number     int
	URL        string
	BaseBranch string
	Open       bool
	Layers     []SCMStackLayer
}

type SCMStackRegisterRequest struct {
	Repo   SCMRepo
	Parent int
	Child  int
}

type SCMStackMergeRequest struct {
	PR              SCMPRRef
	ExpectedHeadSHA string
	Method          SCMMergeMethod
}

type SCMStackMergeStatus string

const (
	SCMStackMerged   SCMStackMergeStatus = "merged"
	SCMStackEnqueued SCMStackMergeStatus = "enqueued"
	SCMStackPending  SCMStackMergeStatus = "pending"
)

type SCMStacker interface {
	PullRequestStack(ctx context.Context, ref SCMPRRef) (SCMStack, bool, error)
	FindOpenPullRequest(ctx context.Context, repo SCMRepo, branch string) (SCMPRRef, bool, error)
	RegisterStacked(ctx context.Context, request SCMStackRegisterRequest) (SCMStack, error)
	MergeStack(ctx context.Context, request SCMStackMergeRequest) (SCMStackMergeStatus, error)
}
