package ports

import "context"

// SCMStackLayer is one pull request of a provider-native stack.
type SCMStackLayer struct {
	Number     int
	Open       bool
	Draft      bool
	Merged     bool
	HeadBranch string
	HeadSHA    string
}

// SCMStack is a provider-native chain of pull requests, each based on the one
// before it. Layers run bottom first, so the last layer is the top of the stack.
type SCMStack struct {
	Number     int
	URL        string
	BaseBranch string
	Open       bool
	Layers     []SCMStackLayer
}

// SCMStackRegisterRequest places Child on top of Parent, starting a stack when
// Parent is in none.
type SCMStackRegisterRequest struct {
	Repo   SCMRepo
	Parent int
	Child  int
}

// SCMStackMergeRequest lands PR and every unmerged layer below it in one
// all-or-nothing operation. ExpectedHeadSHA is PR's head.
type SCMStackMergeRequest struct {
	PR              SCMPRRef
	ExpectedHeadSHA string
	Method          SCMMergeMethod
}

// SCMStackMergeStatus is how far a stack merge got.
type SCMStackMergeStatus string

const (
	// SCMStackMerged means every requested layer has landed.
	SCMStackMerged SCMStackMergeStatus = "merged"
	// SCMStackEnqueued means the layers joined the base branch's merge queue.
	SCMStackEnqueued SCMStackMergeStatus = "enqueued"
	// SCMStackPending means the provider is still working after AO stopped waiting.
	SCMStackPending SCMStackMergeStatus = "pending"
)

// SCMStacker reads and changes provider-native pull request stacks.
type SCMStacker interface {
	// PullRequestStack returns the stack holding ref. The bool is false when ref
	// is in no stack, or the provider has no stacks for this repository.
	PullRequestStack(ctx context.Context, ref SCMPRRef) (SCMStack, bool, error)
	// FindOpenPullRequest returns the one open pull request whose head is
	// branch. The bool is false when there is none or more than one.
	FindOpenPullRequest(ctx context.Context, repo SCMRepo, branch string) (SCMPRRef, bool, error)
	// RegisterStacked records an existing child pull request on top of its parent.
	RegisterStacked(ctx context.Context, request SCMStackRegisterRequest) (SCMStack, error)
	// MergeStack lands the requested layers, waiting a bounded time for the result.
	MergeStack(ctx context.Context, request SCMStackMergeRequest) (SCMStackMergeStatus, error)
}
