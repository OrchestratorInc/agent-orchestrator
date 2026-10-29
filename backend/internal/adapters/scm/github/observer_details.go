package github

import (
	"strings"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// The batch query reads these slow-changing, view-only fields for every tracked
// PR on every tick, so each is capped: the PR view shows a bounded sample, not
// the provider's full history.
const (
	scmDetailsLabelLimit    = 20
	scmDetailsReviewerLimit = 20
	scmDetailsCommitLimit   = 50
)

// pullRequestDetailsFromGraphQL normalizes the description, labels, requested
// reviewers and commits of one batch-query PR node.
func pullRequestDetailsFromGraphQL(pr map[string]any) *domain.PullRequestDetails {
	description, truncated := truncateDescription(str(pr["body"]))
	details := &domain.PullRequestDetails{
		Description:          description,
		DescriptionTruncated: truncated,
		Labels:               []domain.PullRequestLabel{},
		RequestedReviewers:   []domain.PullRequestRequestedReviewer{},
		Commits:              []domain.PullRequestCommit{},
	}
	if labels, _ := pr["labels"].(map[string]any); labels != nil {
		for _, node := range nodes(labels["nodes"]) {
			if name := strings.TrimSpace(str(node["name"])); name != "" {
				details.Labels = append(details.Labels, domain.PullRequestLabel{Name: name, Color: str(node["color"])})
			}
		}
	}
	if requests, _ := pr["reviewRequests"].(map[string]any); requests != nil {
		for _, node := range nodes(requests["nodes"]) {
			reviewer, _ := node["requestedReviewer"].(map[string]any)
			if reviewer == nil {
				continue
			}
			if str(reviewer["__typename"]) == "Team" {
				if name := str(reviewer["name"]); name != "" {
					details.RequestedReviewers = append(details.RequestedReviewers, domain.PullRequestRequestedReviewer{Login: name, IsTeam: true})
				}
				continue
			}
			if login := str(reviewer["login"]); login != "" {
				details.RequestedReviewers = append(details.RequestedReviewers, domain.PullRequestRequestedReviewer{Login: login, AvatarURL: str(reviewer["avatarUrl"])})
			}
		}
	}
	if commits, _ := pr["commitList"].(map[string]any); commits != nil {
		details.CommitCount = int(num(commits["totalCount"]))
		for _, node := range nodes(commits["nodes"]) {
			commit, _ := node["commit"].(map[string]any)
			if commit == nil || str(commit["oid"]) == "" {
				continue
			}
			details.Commits = append(details.Commits, domain.PullRequestCommit{
				SHA:         str(commit["oid"]),
				Headline:    str(commit["messageHeadline"]),
				Author:      commitAuthor(commit["author"]),
				CommittedAt: parseGitHubTime(str(commit["committedDate"])),
			})
		}
		if details.CommitCount < len(details.Commits) {
			details.CommitCount = len(details.Commits)
		}
	}
	return details
}

// commitAuthor prefers the GitHub login and falls back to the git author name,
// which is all a commit made by an unlinked email has.
func commitAuthor(v any) string {
	author, _ := v.(map[string]any)
	if user, _ := author["user"].(map[string]any); user != nil {
		if login := str(user["login"]); login != "" {
			return login
		}
	}
	return str(author["name"])
}

// truncateDescription cuts a PR body to the stored cap without splitting a
// UTF-8 character.
func truncateDescription(body string) (string, bool) {
	if len(body) <= domain.MaxPullRequestDescriptionBytes {
		return body, false
	}
	cut := domain.MaxPullRequestDescriptionBytes
	for cut > 0 && !utf8.RuneStart(body[cut]) {
		cut--
	}
	return body[:cut], true
}
