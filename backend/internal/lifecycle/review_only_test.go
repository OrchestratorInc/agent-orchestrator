package lifecycle

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// #6810: a PR attached only so a --no-inject review could run on it is not
// the session's to fix, so no CI, review, or merge-conflict nudge reaches it.
func TestPRObservation_ReviewOnlyPRSendsNoNudges(t *testing.T) {
	m, st, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")
	st.prs["mer-1"] = []domain.PullRequest{{URL: "pr1", SessionID: "mer-1", ReviewOnly: true}}
	st.comments["pr1"] = []domain.PullRequestComment{{ID: "1", ThreadID: "T1", Author: "alice", File: "a.go", Line: 1, Body: "fix this", AutoInjectReview: true}}
	st.reviews["pr1"] = []domain.PullRequestReview{{ID: "r1", Author: "alice", State: domain.ReviewChangesRequest, Body: "fix", AutoInjectReview: true}}
	o := ports.PRObservation{
		Fetched:      true,
		URL:          "pr1",
		CI:           domain.CIFailing,
		Checks:       []ports.PRCheckObservation{{Name: "build", CommitHash: "c1", Status: domain.PRCheckFailed, LogTail: "boom"}},
		Review:       domain.ReviewChangesRequest,
		Mergeability: domain.MergeConflicting,
	}
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 0 {
		t.Fatalf("review-only PR nudged the session: %v", msg.msgs)
	}

	// The same PR owned by the session gets every nudge.
	st.prs["mer-1"][0].ReviewOnly = false
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) < 3 {
		t.Fatalf("owned PR nudges = %d (%v), want CI, review, and merge-conflict nudges", len(msg.msgs), msg.msgs)
	}
}

func TestSessionCompleteIgnoresReviewOnlyPRs(t *testing.T) {
	m, st, _ := newManager()
	st.sessions["mer-1"] = working("mer-1")
	st.prs["mer-1"] = []domain.PullRequest{
		{URL: "own", SessionID: "mer-1", Merged: true},
		{URL: "reviewed", SessionID: "mer-1", ReviewOnly: true},
	}
	done, err := m.sessionComplete(ctx, "mer-1")
	if err != nil || !done {
		t.Fatalf("done=%v err=%v, want an open review-only PR not to hold the session open", done, err)
	}
	st.prs["mer-1"] = []domain.PullRequest{{URL: "reviewed", SessionID: "mer-1", ReviewOnly: true, Merged: true}}
	if done, err = m.sessionComplete(ctx, "mer-1"); err != nil || done {
		t.Fatalf("done=%v err=%v, want a merged review-only PR not to complete the session", done, err)
	}
}
