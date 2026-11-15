package domain

type SessionStatus string

const (
	StatusWorking          SessionStatus = "working"
	StatusPROpen           SessionStatus = "pr_open"
	StatusDraft            SessionStatus = "draft"
	StatusCIFailed         SessionStatus = "ci_failed"
	StatusReviewPending    SessionStatus = "review_pending"
	StatusChangesRequested SessionStatus = "changes_requested"
	StatusCommented        SessionStatus = "commented"
	StatusApproved         SessionStatus = "approved"
	StatusMergeable        SessionStatus = "mergeable"
	StatusMerged           SessionStatus = "merged"
	StatusNeedsInput       SessionStatus = "needs_input"
	StatusExited           SessionStatus = "exited"
	StatusIdle             SessionStatus = "idle"
	StatusTerminated       SessionStatus = "terminated"
	StatusNoSignal         SessionStatus = "no_signal"
)
