package contract

import "time"

type ActivityState string

const (
	ActivityActive       ActivityState = "active"
	ActivityIdle         ActivityState = "idle"
	ActivityWaitingInput ActivityState = "waiting_input"
	ActivityBlocked      ActivityState = "blocked"
	ActivityExited       ActivityState = "exited"
)

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

type SessionFacts struct {
	Activity       ActivityState
	LastActivityAt time.Time
	HasSignal      bool
	SignalExpected bool
	IsTerminated   bool
}

type CIState string

const (
	CIUnknown CIState = "unknown"
	CIPending CIState = "pending"
	CIPassing CIState = "passing"
	CIFailing CIState = "failing"
)

type ReviewDecision string

const (
	ReviewNone           ReviewDecision = "none"
	ReviewApproved       ReviewDecision = "approved"
	ReviewChangesRequest ReviewDecision = "changes_requested"
	ReviewRequired       ReviewDecision = "review_required"
)

type Mergeability string

const (
	MergeUnknown     Mergeability = "unknown"
	MergeMergeable   Mergeability = "mergeable"
	MergeConflicting Mergeability = "conflicting"
	MergeBlocked     Mergeability = "blocked"
	MergeUnstable    Mergeability = "unstable"
)

type PRFacts struct {
	URL            string
	Draft          bool
	Merged         bool
	Closed         bool
	CI             CIState
	Review         ReviewDecision
	Mergeability   Mergeability
	ReviewComments bool
	SourceBranch   string
	TargetBranch   string
}

type StackPosition struct {
	Blocked       bool
	BottomOfStack bool
}

func DeriveStatus(
	session SessionFacts,
	prs []PRFacts,
	now time.Time,
	noSignalGrace time.Duration,
) SessionStatus {
	if session.IsTerminated {
		if len(openPRs(prs)) == 0 && anyMerged(prs) {
			return StatusMerged
		}
		return StatusTerminated
	}

	switch session.Activity {
	case ActivityActive:
		return StatusWorking
	case ActivityExited:
		return StatusExited
	case ActivityWaitingInput, ActivityBlocked:
		return StatusNeedsInput
	}

	if scmStatus := DeriveSCMStatus(prs); scmStatus != "" {
		return scmStatus
	}

	if silentPastGrace(session, now, noSignalGrace) {
		return StatusNoSignal
	}
	return StatusIdle
}

func silentPastGrace(session SessionFacts, now time.Time, noSignalGrace time.Duration) bool {
	return session.SignalExpected && !session.HasSignal &&
		now.Sub(session.LastActivityAt) > noSignalGrace
}

func DeriveSCMStatus(prs []PRFacts) SessionStatus {
	open := openPRs(prs)
	if len(open) > 0 {
		return aggregatePRStatus(open)
	}
	if anyMerged(prs) {
		return StatusMerged
	}
	return ""
}

func BuildStacks(prs []PRFacts) map[string]StackPosition {
	openSources := make(map[string]bool, len(prs))
	for _, pr := range prs {
		if !pr.Merged && !pr.Closed && pr.SourceBranch != "" {
			openSources[pr.SourceBranch] = true
		}
	}

	positions := make(map[string]StackPosition, len(prs))
	for _, pr := range prs {
		blocked := pr.TargetBranch != "" && openSources[pr.TargetBranch]
		positions[pr.URL] = StackPosition{
			Blocked:       blocked,
			BottomOfStack: !blocked,
		}
	}
	return positions
}

func openPRs(prs []PRFacts) []PRFacts {
	open := make([]PRFacts, 0, len(prs))
	for _, pr := range prs {
		if !pr.Merged && !pr.Closed {
			open = append(open, pr)
		}
	}
	return open
}

func anyMerged(prs []PRFacts) bool {
	for _, pr := range prs {
		if pr.Merged {
			return true
		}
	}
	return false
}

func aggregatePRStatus(open []PRFacts) SessionStatus {
	stacks := BuildStacks(open)
	candidates := make([]SessionStatus, 0, len(open))
	for _, pr := range open {
		status := prPipelineStatus(pr)
		if stacks[pr.URL].Blocked && !isActionableChildSignal(status) {
			continue
		}
		candidates = append(candidates, status)
	}
	if len(candidates) == 0 {
		for _, pr := range open {
			candidates = append(candidates, prPipelineStatus(pr))
		}
	}

	worst := candidates[0]
	for _, status := range candidates[1:] {
		if statusSeverity(status) < statusSeverity(worst) {
			worst = status
		}
	}
	return worst
}

func isActionableChildSignal(status SessionStatus) bool {
	switch status {
	case StatusCIFailed, StatusDraft, StatusChangesRequested, StatusCommented:
		return true
	default:
		return false
	}
}

func statusSeverity(status SessionStatus) int {
	switch status {
	case StatusCIFailed:
		return 0
	case StatusChangesRequested:
		return 1
	case StatusCommented:
		return 2
	case StatusDraft:
		return 3
	case StatusReviewPending:
		return 4
	case StatusPROpen:
		return 5
	case StatusApproved:
		return 6
	case StatusMergeable:
		return 7
	default:
		return 8
	}
}

func prPipelineStatus(pr PRFacts) SessionStatus {
	switch {
	case pr.CI == CIFailing:
		return StatusCIFailed
	case pr.Draft:
		return StatusDraft
	case pr.Review == ReviewChangesRequest:
		return StatusChangesRequested
	case pr.ReviewComments:
		return StatusCommented
	case pr.Mergeability == MergeMergeable:
		return StatusMergeable
	case pr.Review == ReviewRequired:
		return StatusReviewPending
	case pr.Mergeability == MergeBlocked:
		return StatusPROpen
	case pr.Review == ReviewApproved:
		return StatusApproved
	default:
		return StatusPROpen
	}
}
