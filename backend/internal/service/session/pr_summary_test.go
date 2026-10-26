package session

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// A terminal PR has no mergeability: sessions whose agent already exited are
// never re-observed, so a conflicting verdict latched before the merge must
// not be re-reported by the summary.
func TestSummarizeMergeabilityTerminalPRsAreUnknown(t *testing.T) {
	cases := []struct {
		name string
		pr   domain.PullRequest
		want domain.Mergeability
	}{
		{name: "merged ignores latched conflicting", pr: domain.PullRequest{Mergeability: domain.MergeConflicting, Merged: true}, want: domain.MergeUnknown},
		{name: "closed ignores latched conflicting", pr: domain.PullRequest{Mergeability: domain.MergeConflicting, Closed: true}, want: domain.MergeUnknown},
		{name: "open conflicting is reported", pr: domain.PullRequest{Mergeability: domain.MergeConflicting}, want: domain.MergeConflicting},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := summarizeMergeability(tc.pr, nil)
			if got.State != tc.want {
				t.Fatalf("State = %q, want %q", got.State, tc.want)
			}
			if tc.pr.Merged || tc.pr.Closed {
				if len(got.Reasons) != 0 {
					t.Fatalf("Reasons = %v, want none for terminal PR", got.Reasons)
				}
			}
		})
	}
}
