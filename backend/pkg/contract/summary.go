package contract

// SummaryFacts are the server-owned facts used to derive a short Kanban card
// summary at read time. Conversation content is intentionally not part of this
// contract, so prompts and agent/tool protocol output cannot leak into cards.
type SummaryFacts struct {
	// OpenPRs counts pull requests that are neither merged nor closed.
	OpenPRs int
	// CIFailing reports whether any open PR has failing checks.
	CIFailing bool
	// DisplayStatus is the column phrase already derived for the card. It is
	// the fallback when no richer fact is available.
	DisplayStatus DisplayStatus
}

// SummarizeSession derives the generic activity line for a Kanban card from
// server-owned state only. Conversation prompts and assistant/tool output are
// deliberately excluded: they are not summaries and may contain protocol
// markup or implementation chatter. Empty means the card renders no summary.
func SummarizeSession(facts SummaryFacts) string {
	if pr := prSummary(facts); pr != "" {
		return pr
	}
	return statusSummary(facts.DisplayStatus)
}

func statusSummary(status DisplayStatus) string {
	switch status {
	case DisplayWorking, DisplayFixingCI, DisplayAddressingComments,
		DisplayAwaitingPR, DisplayDraft, DisplayReviewing:
		return "Working"
	case DisplayBlocked, DisplayNoSignal, DisplayExited,
		DisplayNeedsReview, DisplayReviewPending, DisplayReviewScheduled,
		DisplayCIFailing, DisplayCommented, DisplayChangesRequested,
		DisplayNeedsHumanReview, DisplayMergeable, DisplayApproved,
		DisplayMerged, DisplayClosed, DisplayTerminated:
		return "Stalled"
	default:
		return ""
	}
}

func prSummary(facts SummaryFacts) string {
	switch {
	case facts.CIFailing:
		return "CI failing on open PR"
	case facts.OpenPRs > 0:
		return "PR open"
	default:
		return ""
	}
}
