package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.SCMCommenter = (*Provider)(nil)

// PostComment posts a top-level conversation comment through the REST issue
// comments endpoint, or replies in a review thread through GraphQL when
// request.ThreadID is set. The returned ID is the GraphQL node id in both
// cases, matching the ids the observer stores.
func (p *Provider) PostComment(ctx context.Context, request ports.SCMCommentRequest) (ports.SCMCommentResult, error) {
	if p == nil || p.client == nil {
		return ports.SCMCommentResult{}, fmt.Errorf("github scm: comment provider is not configured")
	}
	if request.PR.Number <= 0 || strings.TrimSpace(request.PR.Repo.Owner) == "" || strings.TrimSpace(request.PR.Repo.Name) == "" {
		return ports.SCMCommentResult{}, fmt.Errorf("github scm: invalid pull request reference")
	}
	if strings.TrimSpace(request.Body) == "" {
		return ports.SCMCommentResult{}, fmt.Errorf("github scm: comment body is required")
	}
	if threadID := strings.TrimSpace(request.ThreadID); threadID != "" {
		return p.replyToReviewThread(ctx, threadID, request.Body)
	}
	return p.postConversationComment(ctx, request.PR, request.Body)
}

func (p *Provider) postConversationComment(ctx context.Context, ref ports.SCMPRRef, body string) (ports.SCMCommentResult, error) {
	payload := struct {
		Body string `json:"body"`
	}{Body: body}
	resp, err := p.client.doREST(ctx, http.MethodPost,
		repoPath(ref.Repo.Owner, ref.Repo.Name, "issues", strconv.Itoa(ref.Number), "comments"), nil, payload)
	if err != nil {
		return ports.SCMCommentResult{}, mapMutationStatus(resp.StatusCode, err)
	}
	var result struct {
		ID        int64  `json:"id"`
		NodeID    string `json:"node_id"`
		HTMLURL   string `json:"html_url"`
		Body      string `json:"body"`
		CreatedAt string `json:"created_at"`
		User      struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"user"`
	}
	if err := json.Unmarshal(resp.Body, &result); err != nil {
		return ports.SCMCommentResult{}, fmt.Errorf("github scm: decode comment response: %w", err)
	}
	id := strings.TrimSpace(result.NodeID)
	if id == "" && result.ID > 0 {
		id = strconv.FormatInt(result.ID, 10)
	}
	if id == "" {
		return ports.SCMCommentResult{}, fmt.Errorf("github scm: comment response missing id")
	}
	return ports.SCMCommentResult{
		ID:        id,
		URL:       result.HTMLURL,
		Author:    result.User.Login,
		IsBot:     strings.EqualFold(result.User.Type, "Bot"),
		Body:      firstNonEmpty(result.Body, body),
		CreatedAt: parseGitHubTime(result.CreatedAt),
	}, nil
}

func (p *Provider) replyToReviewThread(ctx context.Context, threadID, body string) (ports.SCMCommentResult, error) {
	data, err := p.client.doGraphQL(ctx, `mutation ReplyToReviewThread($threadId: ID!, $body: String!) {
  addPullRequestReviewThreadReply(input: { pullRequestReviewThreadId: $threadId, body: $body }) {
    comment { id url body createdAt author { login __typename } }
  }
}`, map[string]any{"threadId": threadID, "body": body})
	if err != nil {
		return ports.SCMCommentResult{}, mapGraphQLMutationError(err)
	}
	mutation, _ := data["addPullRequestReviewThreadReply"].(map[string]any)
	comment, _ := mutation["comment"].(map[string]any)
	id := strings.TrimSpace(str(comment["id"]))
	if id == "" {
		return ports.SCMCommentResult{}, fmt.Errorf("github scm: review thread reply returned no comment")
	}
	author, _ := comment["author"].(map[string]any)
	return ports.SCMCommentResult{
		ID:        id,
		URL:       str(comment["url"]),
		Author:    str(author["login"]),
		IsBot:     isBotAuthor(author),
		Body:      firstNonEmpty(str(comment["body"]), body),
		CreatedAt: parseGitHubTime(str(comment["createdAt"])),
	}, nil
}
