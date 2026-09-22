package session

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func intPtr(value int) *int { return &value }

func TestClassifyDeliveryStatesAndSafeActions(t *testing.T) {
	openPR := domain.PullRequest{URL: "https://github.com/acme/widget/pull/42", Number: 42, Provider: "github", Repo: "acme/widget", SourceBranch: "ao/card"}
	cases := []struct {
		name    string
		files   WorkspaceFiles
		facts   deliveryGitFacts
		prs     []domain.PullRequest
		state   DeliveryState
		action  DeliveryAction
		blocked bool
	}{
		{name: "empty", files: WorkspaceFiles{}, facts: deliveryGitFacts{branch: "ao/card", remoteRepo: "acme/widget"}, state: DeliveryStateEmpty},
		{name: "uncommitted no pr", files: WorkspaceFiles{CompareBaseRef: "origin/main", Sections: WorkspaceFileSections{Unstaged: []WorkspaceFileSummary{{Path: "a.go"}}}, Summary: WorkspaceSummary{Files: 1}}, facts: deliveryGitFacts{branch: "ao/card", remoteRepo: "acme/widget"}, state: DeliveryStateUncommitted, action: DeliveryActionCommitAndPublish},
		{name: "committed no pr", files: WorkspaceFiles{CompareBaseRef: "origin/main", Commits: []CommitSummary{{SHA: "abc", Subject: "feat: card"}}, Summary: WorkspaceSummary{Files: 1}, Ahead: intPtr(1), Behind: intPtr(0)}, facts: deliveryGitFacts{branch: "ao/card", remoteRepo: "acme/widget"}, state: DeliveryStateReadyToPublish, action: DeliveryActionPublishPR},
		{name: "unknown target branch", files: WorkspaceFiles{Commits: []CommitSummary{{SHA: "abc"}}, Summary: WorkspaceSummary{Files: 1}}, facts: deliveryGitFacts{branch: "ao/card", remoteRepo: "acme/widget"}, state: DeliveryStateBlocked, blocked: true},
		{name: "dirty pr", files: WorkspaceFiles{Sections: WorkspaceFileSections{Untracked: []WorkspaceFileSummary{{Path: "new.go"}}}, Summary: WorkspaceSummary{Files: 1}}, facts: deliveryGitFacts{branch: "ao/card", remoteRepo: "acme/widget"}, prs: []domain.PullRequest{openPR}, state: DeliveryStateUncommittedForPR, action: DeliveryActionCommitAndPush},
		{name: "ahead pr", files: WorkspaceFiles{Commits: []CommitSummary{{SHA: "def", Subject: "fix: follow-up"}}, Ahead: intPtr(2), Behind: intPtr(0)}, facts: deliveryGitFacts{branch: "ao/card", remoteRepo: "acme/widget"}, prs: []domain.PullRequest{openPR}, state: DeliveryStateAheadOfPR, action: DeliveryActionPush},
		{name: "synchronized pr", files: WorkspaceFiles{Ahead: intPtr(0), Behind: intPtr(0)}, facts: deliveryGitFacts{branch: "ao/card", remoteRepo: "acme/widget"}, prs: []domain.PullRequest{openPR}, state: DeliveryStateSynchronized},
		{name: "diverged", files: WorkspaceFiles{Commits: []CommitSummary{{SHA: "abc"}}, Ahead: intPtr(1), Behind: intPtr(1)}, facts: deliveryGitFacts{branch: "ao/card", remoteRepo: "acme/widget"}, state: DeliveryStateBlocked, blocked: true},
		{name: "detached", files: WorkspaceFiles{Commits: []CommitSummary{{SHA: "abc"}}}, facts: deliveryGitFacts{detached: true, remoteRepo: "acme/widget"}, state: DeliveryStateBlocked, blocked: true},
		{name: "missing remote", files: WorkspaceFiles{Commits: []CommitSummary{{SHA: "abc"}}}, facts: deliveryGitFacts{branch: "ao/card"}, state: DeliveryStateBlocked, blocked: true},
		{name: "ambiguous prs", files: WorkspaceFiles{}, facts: deliveryGitFacts{branch: "ao/card", remoteRepo: "acme/widget"}, prs: []domain.PullRequest{openPR, {URL: "https://github.com/acme/widget/pull/43", Number: 43, Repo: "acme/widget", SourceBranch: "ao/card"}}, state: DeliveryStateBlocked, blocked: true},
		{name: "mismatched pr branch", files: WorkspaceFiles{}, facts: deliveryGitFacts{branch: "ao/card", remoteRepo: "acme/widget"}, prs: []domain.PullRequest{{URL: openPR.URL, Number: 42, Repo: "acme/widget", SourceBranch: "other"}}, state: DeliveryStateBlocked, blocked: true},
		{name: "mismatched pr repo", files: WorkspaceFiles{}, facts: deliveryGitFacts{branch: "ao/card", remoteRepo: "acme/widget"}, prs: []domain.PullRequest{{URL: openPR.URL, Number: 42, Repo: "acme/other", SourceBranch: "ao/card"}}, state: DeliveryStateBlocked, blocked: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyDelivery(tc.files, tc.facts, tc.prs)
			if got.State != tc.state || got.Action != tc.action {
				t.Fatalf("delivery = state %q action %q, want %q %q", got.State, got.Action, tc.state, tc.action)
			}
			if tc.blocked && got.BlockedReason == "" {
				t.Fatal("blocked delivery omitted its reason")
			}
			if tc.action != "" && got.WorkspaceVersion != tc.files.WorkspaceVersion {
				t.Fatalf("workspace version = %q, want %q", got.WorkspaceVersion, tc.files.WorkspaceVersion)
			}
		})
	}
}

func TestClassifyUnsupportedDeliveryBlocksWorkButKeepsEmptyStateQuiet(t *testing.T) {
	blocked := classifyUnsupportedDelivery(WorkspaceFiles{WorkspaceVersion: "v1", Files: []WorkspaceFileSummary{{Path: "note.txt"}}, Summary: WorkspaceSummary{Files: 1}}, "unsupported")
	if blocked.State != DeliveryStateBlocked || blocked.BlockedReason != "unsupported" || blocked.WorkspaceVersion != "v1" {
		t.Fatalf("blocked = %#v", blocked)
	}
	empty := classifyUnsupportedDelivery(WorkspaceFiles{WorkspaceVersion: "v2"}, "unsupported")
	if empty.State != DeliveryStateEmpty || empty.BlockedReason != "" {
		t.Fatalf("empty = %#v", empty)
	}
}
