package domain

import (
	"testing"
	"time"
)

func TestPullRequestAutomaticWorkRequiresOwnershipEvidence(t *testing.T) {
	created := time.Date(2026, 1, 20, 12, 0, 0, 500_000_000, time.UTC)
	session := SessionRecord{ID: "p-1", CreatedAt: created}
	for _, tc := range []struct {
		name string
		pr   PullRequest
		want bool
	}{
		{"old automatic", PullRequest{SessionID: session.ID, CreatedAtProvider: created.Add(-24 * time.Hour), OwnershipSource: PROwnershipDiscovered}, false},
		{"unknown date", PullRequest{SessionID: session.ID}, false},
		{"provider second precision", PullRequest{SessionID: session.ID, CreatedAtProvider: created.Truncate(time.Second)}, true},
		{"current", PullRequest{SessionID: session.ID, CreatedAtProvider: created.Add(time.Minute)}, true},
		{"explicit old claim", PullRequest{SessionID: session.ID, CreatedAtProvider: created.Add(-24 * time.Hour), OwnershipSource: PROwnershipClaimed}, true},
		{"other owner", PullRequest{SessionID: "p-2", OwnershipSource: PROwnershipClaimed}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.pr.AllowsAutomaticWork(session); got != tc.want {
				t.Fatalf("automatic work=%v, want %v", got, tc.want)
			}
		})
	}
}
