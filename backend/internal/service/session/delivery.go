package session

import (
	"context"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// DeliveryState describes the next safe stage between local work and a PR.
type DeliveryState string

const (
	DeliveryStateEmpty            DeliveryState = "empty"
	DeliveryStateUncommitted      DeliveryState = "uncommitted"
	DeliveryStateReadyToPublish   DeliveryState = "ready_to_publish"
	DeliveryStateUncommittedForPR DeliveryState = "uncommitted_for_pr"
	DeliveryStateAheadOfPR        DeliveryState = "ahead_of_pr"
	DeliveryStateSynchronized     DeliveryState = "synchronized"
	DeliveryStateBlocked          DeliveryState = "blocked"
)

// DeliveryAction is the one mutation AO can safely offer for a delivery state.
type DeliveryAction string

const (
	DeliveryActionCommitAndPublish DeliveryAction = "commit_and_publish_pr"
	DeliveryActionPublishPR        DeliveryAction = "publish_pr"
	DeliveryActionCommitAndPush    DeliveryAction = "commit_and_push"
	DeliveryActionPush             DeliveryAction = "push"
)

// DeliveryPullRequest identifies the exact session-owned target for updates.
type DeliveryPullRequest struct {
	URL    string
	Number int
}

// DeliveryStatus is the daemon-authoritative summary consumed by the inspector.
type DeliveryStatus struct {
	State            DeliveryState
	Action           DeliveryAction
	BlockedReason    string
	WorkspaceVersion string
	Branch           string
	Repository       string
	CommitCount      int
	CommitSubject    string
	ChangedFiles     int
	Additions        int
	Deletions        int
	Ahead            *int
	Behind           *int
	PullRequest      *DeliveryPullRequest
}

type deliveryGitFacts struct {
	branch     string
	remoteRepo string
	detached   bool
}

func readDeliveryGitFacts(ctx context.Context, root string) deliveryGitFacts {
	branchOut, branchErr := gitWorkspaceOutput(ctx, root, "branch", "--show-current")
	branch := strings.TrimSpace(branchOut)
	remoteOut, remoteErr := gitWorkspaceOutput(ctx, root, "remote", "get-url", "origin")
	facts := deliveryGitFacts{branch: branch, detached: branchErr != nil || branch == ""}
	if remoteErr == nil {
		facts.remoteRepo = normalizeDeliveryRepo(remoteOut)
	}
	return facts
}

func normalizeDeliveryRepo(remote string) string {
	remote = strings.TrimSpace(strings.TrimSuffix(remote, ".git"))
	if i := strings.Index(remote, "://"); i >= 0 {
		parts := strings.Split(strings.Trim(remote[i+3:], "/"), "/")
		if len(parts) >= 3 {
			return strings.Join(parts[1:], "/")
		}
	}
	if i := strings.Index(remote, ":"); i >= 0 {
		return strings.Trim(remote[i+1:], "/")
	}
	parts := strings.Split(strings.Trim(remote, "/"), "/")
	if len(parts) >= 2 {
		return strings.Join(parts[len(parts)-2:], "/")
	}
	return ""
}

func classifyUnsupportedDelivery(files WorkspaceFiles, reason string) DeliveryStatus {
	if len(files.Files) == 0 && files.Summary.Files == 0 {
		return DeliveryStatus{State: DeliveryStateEmpty, WorkspaceVersion: files.WorkspaceVersion}
	}
	return DeliveryStatus{
		State: DeliveryStateBlocked, BlockedReason: reason, WorkspaceVersion: files.WorkspaceVersion,
		ChangedFiles: files.Summary.Files, Additions: files.Summary.Additions, Deletions: files.Summary.Deletions,
	}
}

func classifyDelivery(files WorkspaceFiles, facts deliveryGitFacts, prs []domain.PullRequest) DeliveryStatus {
	status := DeliveryStatus{
		WorkspaceVersion: files.WorkspaceVersion,
		Branch:           strings.TrimSpace(facts.branch), Repository: strings.TrimSpace(facts.remoteRepo),
		CommitCount: len(files.Commits), ChangedFiles: files.Summary.Files,
		Additions: files.Summary.Additions, Deletions: files.Summary.Deletions,
		Ahead: files.Ahead, Behind: files.Behind,
	}
	if len(files.Commits) > 0 {
		status.CommitSubject = files.Commits[0].Subject
	}
	dirty := len(files.Sections.Staged)+len(files.Sections.Unstaged)+len(files.Sections.Untracked) > 0
	hasWork := dirty || len(files.Commits) > 0 || files.Summary.Files > 0
	open := make([]domain.PullRequest, 0, len(prs))
	for _, pr := range prs {
		if !pr.Closed && !pr.Merged {
			open = append(open, pr)
		}
	}
	if !hasWork && len(open) == 0 {
		status.State = DeliveryStateEmpty
		return status
	}
	block := func(reason string) DeliveryStatus {
		status.State = DeliveryStateBlocked
		status.BlockedReason = reason
		return status
	}
	if facts.detached || status.Branch == "" {
		return block("The session workspace is on a detached HEAD.")
	}
	if status.Repository == "" {
		return block("No origin remote is configured for this workspace.")
	}
	if files.Behind != nil && *files.Behind > 0 {
		return block("The local branch has diverged from its remote; AO will not force-push or rebase it.")
	}
	if len(open) > 1 {
		return block("More than one open pull request is associated with this session.")
	}
	if len(open) == 1 {
		pr := open[0]
		if strings.TrimSpace(pr.SourceBranch) != status.Branch || (strings.TrimSpace(pr.Repo) != "" && strings.TrimSpace(pr.Repo) != status.Repository) {
			return block("The session branch does not unambiguously match its pull request.")
		}
		status.PullRequest = &DeliveryPullRequest{URL: pr.URL, Number: pr.Number}
		if dirty {
			status.State, status.Action = DeliveryStateUncommittedForPR, DeliveryActionCommitAndPush
			return status
		}
		if files.Ahead != nil && *files.Ahead > 0 {
			status.State, status.Action = DeliveryStateAheadOfPR, DeliveryActionPush
			return status
		}
		status.State = DeliveryStateSynchronized
		return status
	}
	target := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(files.CompareBaseRef), "refs/remotes/"), "origin/")
	if target == "" || target == "HEAD" {
		return block("The pull request target branch cannot be determined safely.")
	}
	if dirty {
		status.State, status.Action = DeliveryStateUncommitted, DeliveryActionCommitAndPublish
		return status
	}
	status.State, status.Action = DeliveryStateReadyToPublish, DeliveryActionPublishPR
	return status
}
