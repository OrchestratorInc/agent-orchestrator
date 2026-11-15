package contract

import "time"

type KanbanColumn string

const (
	KanbanBuilding    KanbanColumn = "building"
	KanbanValidating  KanbanColumn = "validating"
	KanbanNeedsReview KanbanColumn = "needs_review"
	KanbanReady       KanbanColumn = "ready"
	KanbanArchive     KanbanColumn = "archive"
)

type KanbanSessionFacts struct {
	SessionFacts
	Interrupted       bool
	HasArtifactOutput bool
	AutoReview        bool
	AutoInjectReview  bool
	AutoInjectCI      bool
}

type KanbanReviewRunFacts struct {
	Present          bool
	Running          bool
	ChangesRequested bool
	Outcome          bool
	Failed           bool
	Cancelled        bool
	RetryExhausted   bool
}

type KanbanExternalReviewFacts struct {
	Approved         bool
	ChangesRequested bool
	Comments         bool
}

type KanbanPRFacts struct {
	URL            string
	Draft          bool
	Merged         bool
	Closed         bool
	CI             CIState
	Review         ReviewDecision
	Mergeability   Mergeability
	UpdatedAt      time.Time
	ReviewRun      KanbanReviewRunFacts
	ExternalReview KanbanExternalReviewFacts
}

func derivePRKanbanColumn(session KanbanSessionFacts, pr KanbanPRFacts) KanbanColumn {
	switch {
	case pr.Merged || pr.Closed:
		return KanbanReady
	case pr.Draft:
		return KanbanValidating
	case externallyApproved(pr) && !mergeBlockedByPR(pr):
		return KanbanReady
	case aoOwnsNextStep(session, pr):
		return KanbanValidating
	case externallyApproved(pr):
		return KanbanNeedsReview
	case pr.ReviewRun.ChangesRequested:
		return KanbanValidating
	case session.AutoReview && (!approvedByAO(pr) || pr.ReviewRun.Cancelled):
		return KanbanValidating
	case pr.ReviewRun.Cancelled || (pr.ReviewRun.Failed && !pr.ReviewRun.Outcome):
		return KanbanNeedsReview
	case pr.Mergeability == MergeMergeable:
		return KanbanReady
	default:
		return KanbanNeedsReview
	}
}

func approvedByAO(pr KanbanPRFacts) bool {
	return pr.ReviewRun.Outcome && !pr.ReviewRun.ChangesRequested
}

func externallyApproved(pr KanbanPRFacts) bool {
	return pr.Review == ReviewApproved && pr.ExternalReview.Approved
}

func mergeBlockedByPR(pr KanbanPRFacts) bool {
	return pr.Mergeability == MergeConflicting || pr.CI == CIFailing
}

func aoOwnsNextStep(session KanbanSessionFacts, pr KanbanPRFacts) bool {
	if pr.ReviewRun.Running {
		return true
	}
	if session.AutoInjectReview && pr.ReviewRun.ChangesRequested {
		return true
	}
	return session.AutoInjectCI && pr.CI == CIFailing
}

func liveKanbanPRs(prs []KanbanPRFacts) []KanbanPRFacts {
	live := make([]KanbanPRFacts, 0, len(prs))
	for _, pr := range prs {
		if !pr.Merged && !pr.Closed {
			live = append(live, pr)
		}
	}
	return live
}

func outranksKanban(candidate KanbanColumn, pr KanbanPRFacts, current KanbanColumn, chosen KanbanPRFacts) bool {
	if kanbanPriority(candidate) != kanbanPriority(current) {
		return kanbanPriority(candidate) < kanbanPriority(current)
	}
	if !pr.UpdatedAt.Equal(chosen.UpdatedAt) {
		return pr.UpdatedAt.After(chosen.UpdatedAt)
	}
	return pr.URL < chosen.URL
}

func kanbanPriority(column KanbanColumn) int {
	switch column {
	case KanbanReady:
		return 0
	case KanbanNeedsReview:
		return 1
	case KanbanValidating:
		return 2
	default:
		return 3
	}
}

type DisplayStatus string

const (
	DisplayWorking            DisplayStatus = "Working"
	DisplayBlocked            DisplayStatus = "Blocked"
	DisplayNeedsInput         DisplayStatus = "Needs input"
	DisplayExited             DisplayStatus = "Exited"
	DisplayNoSignal           DisplayStatus = "No signal"
	DisplayInterrupted        DisplayStatus = "Interrupted"
	DisplayIdle               DisplayStatus = "Idle"
	DisplayFixingCI           DisplayStatus = "Fixing CI failures"
	DisplayAddressingComments DisplayStatus = "Addressing comments"
	DisplayReviewScheduled    DisplayStatus = "Review scheduled"
	DisplayReviewing          DisplayStatus = "Reviewing"
	DisplayRetryingReview     DisplayStatus = "Retrying review"
	DisplayDraft              DisplayStatus = "Draft"
	DisplayAwaitingReview     DisplayStatus = "Awaiting review"
	DisplayReviewFailed       DisplayStatus = "Review failed"
	DisplayReviewCancelled    DisplayStatus = "Review cancelled"
	DisplayCIFailing          DisplayStatus = "CI failing"
	DisplayMergeConflicts     DisplayStatus = "Merge conflicts"
	DisplayCommented          DisplayStatus = "Commented"
	DisplayChangesRequested   DisplayStatus = "Changes requested"
	DisplayMergeable          DisplayStatus = "Mergeable"
	DisplayApproved           DisplayStatus = "Approved"
	DisplayMerged             DisplayStatus = "Merged"
	DisplayClosed             DisplayStatus = "Closed without merge"
	DisplayTerminated         DisplayStatus = "Terminated"
)

type KanbanPresentation struct {
	Column        KanbanColumn
	DisplayStatus DisplayStatus
}

func DeriveKanbanPresentation(
	session KanbanSessionFacts,
	prs []KanbanPRFacts,
	now time.Time,
	noSignalGrace time.Duration,
) KanbanPresentation {
	if session.IsTerminated {
		return KanbanPresentation{Column: KanbanArchive, DisplayStatus: DisplayTerminated}
	}
	if len(prs) == 0 && session.HasArtifactOutput {
		return KanbanPresentation{
			Column:        KanbanValidating,
			DisplayStatus: artifactDisplayStatus(session, now, noSignalGrace),
		}
	}
	if len(prs) == 0 {
		return KanbanPresentation{
			Column:        KanbanBuilding,
			DisplayStatus: buildingDisplayStatus(session, now, noSignalGrace),
		}
	}
	pool := liveKanbanPRs(prs)
	if len(pool) == 0 {
		pool = prs
	}

	column := KanbanColumn("")
	var chosen KanbanPRFacts
	for _, pr := range pool {
		candidate := derivePRKanbanColumn(session, pr)
		if column == "" || outranksKanban(candidate, pr, column, chosen) {
			column, chosen = candidate, pr
		}
	}
	return KanbanPresentation{
		Column:        column,
		DisplayStatus: displayStatusInColumn(column, session, chosen, now, noSignalGrace),
	}
}

func displayStatusInColumn(
	column KanbanColumn,
	session KanbanSessionFacts,
	pr KanbanPRFacts,
	now time.Time,
	noSignalGrace time.Duration,
) DisplayStatus {
	switch column {
	case KanbanValidating:
		return validatingDisplayStatus(session, pr, now, noSignalGrace)
	case KanbanNeedsReview:
		return inReviewDisplayStatus(session, pr, now, noSignalGrace)
	case KanbanReady:
		return readyDisplayStatus(session, pr, now, noSignalGrace)
	default:
		return buildingDisplayStatus(session, now, noSignalGrace)
	}
}

func workerNeedsAttention(session KanbanSessionFacts, now time.Time, noSignalGrace time.Duration) (DisplayStatus, bool) {
	switch {
	case session.Activity == ActivityBlocked:
		return DisplayBlocked, true
	case session.Activity == ActivityWaitingInput:
		return DisplayNeedsInput, true
	case session.Activity == ActivityExited:
		return DisplayExited, true
	case silentPastGrace(session.SessionFacts, now, noSignalGrace):
		return DisplayNoSignal, true
	default:
		return "", false
	}
}

func buildingDisplayStatus(session KanbanSessionFacts, now time.Time, noSignalGrace time.Duration) DisplayStatus {
	return noPRDisplayStatus(session, now, noSignalGrace, DisplayIdle)
}

func artifactDisplayStatus(session KanbanSessionFacts, now time.Time, noSignalGrace time.Duration) DisplayStatus {
	return noPRDisplayStatus(session, now, noSignalGrace, DisplayAwaitingReview)
}

func noPRDisplayStatus(session KanbanSessionFacts, now time.Time, noSignalGrace time.Duration, settled DisplayStatus) DisplayStatus {
	if session.Activity == ActivityActive {
		return DisplayWorking
	}
	if status, ok := workerNeedsAttention(session, now, noSignalGrace); ok {
		return status
	}
	if interruptedWhileIdle(session) {
		return DisplayInterrupted
	}
	return settled
}

func validatingDisplayStatus(session KanbanSessionFacts, pr KanbanPRFacts, now time.Time, noSignalGrace time.Duration) DisplayStatus {
	if status, ok := workerNeedsAttention(session, now, noSignalGrace); ok {
		return status
	}
	active := session.Activity == ActivityActive
	switch {
	case pr.CI == CIFailing && session.AutoInjectCI && active:
		return DisplayFixingCI
	case changesRequestedOn(pr) && session.AutoInjectReview && active:
		return DisplayAddressingComments
	case pr.ReviewRun.Running:
		return DisplayReviewing
	case active:
		return DisplayWorking
	case pr.CI == CIFailing:
		return DisplayCIFailing
	case pr.ReviewRun.ChangesRequested:
		return DisplayChangesRequested
	case pr.Draft:
		return DisplayDraft
	case pr.ReviewRun.Failed && !pr.ReviewRun.Outcome:
		if pr.ReviewRun.RetryExhausted {
			return DisplayReviewFailed
		}
		return DisplayRetryingReview
	case pr.ReviewRun.Cancelled:
		return DisplayReviewCancelled
	default:
		return DisplayReviewScheduled
	}
}

func interruptedWhileIdle(session KanbanSessionFacts) bool {
	return session.Interrupted && session.Activity == ActivityIdle
}

func inReviewDisplayStatus(session KanbanSessionFacts, pr KanbanPRFacts, now time.Time, noSignalGrace time.Duration) DisplayStatus {
	if status, ok := workerNeedsAttention(session, now, noSignalGrace); ok {
		return status
	}
	active := session.Activity == ActivityActive
	switch {
	case pr.CI == CIFailing && session.AutoInjectCI && active:
		return DisplayFixingCI
	case (changesRequestedOn(pr) || pr.ExternalReview.Comments) && session.AutoInjectReview && active:
		return DisplayAddressingComments
	case active:
		return DisplayWorking
	case pr.CI == CIFailing:
		return DisplayCIFailing
	case pr.Mergeability == MergeConflicting:
		return DisplayMergeConflicts
	case changesRequestedOn(pr):
		return DisplayChangesRequested
	case pr.ExternalReview.Comments:
		return DisplayCommented
	case pr.ReviewRun.Failed && !pr.ReviewRun.Outcome:
		return DisplayReviewFailed
	case pr.ReviewRun.Cancelled:
		return DisplayReviewCancelled
	default:
		return DisplayAwaitingReview
	}
}

func changesRequestedOn(pr KanbanPRFacts) bool {
	return pr.ReviewRun.ChangesRequested || pr.ExternalReview.ChangesRequested
}

func readyDisplayStatus(session KanbanSessionFacts, pr KanbanPRFacts, now time.Time, noSignalGrace time.Duration) DisplayStatus {
	switch {
	case pr.Merged:
		return DisplayMerged
	case pr.Closed:
		return DisplayClosed
	}
	if status, ok := workerNeedsAttention(session, now, noSignalGrace); ok {
		return status
	}
	switch {
	case session.Activity == ActivityActive:
		return DisplayWorking
	case pr.Mergeability == MergeMergeable:
		return DisplayMergeable
	default:
		return DisplayApproved
	}
}
