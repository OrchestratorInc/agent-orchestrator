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

var _ ports.SCMPREditor = (*Provider)(nil)

// PullRequestContent reads a pull request's title and whole description, and
// whether the signed-in account may edit them (its author, or anyone who can
// write to the repository).
func (p *Provider) PullRequestContent(ctx context.Context, ref ports.SCMPRRef) (ports.SCMPRContent, error) {
	if p == nil || p.client == nil {
		return ports.SCMPRContent{}, fmt.Errorf("github scm: edit provider is not configured")
	}
	if err := validEditRef(ref); err != nil {
		return ports.SCMPRContent{}, err
	}
	data, err := p.client.doGraphQL(ctx, `query PullRequestContent($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) { pullRequest(number: $number) { title body updatedAt viewerCanUpdate } }
}`, map[string]any{"owner": ref.Repo.Owner, "name": ref.Repo.Name, "number": ref.Number})
	if err != nil {
		return ports.SCMPRContent{}, err
	}
	repo, _ := data["repository"].(map[string]any)
	pr, _ := repo["pullRequest"].(map[string]any)
	if pr == nil {
		return ports.SCMPRContent{}, fmt.Errorf("%w: pull request %s#%d", ErrNotFound, repoFullName(ref.Repo), ref.Number)
	}
	canEdit, _ := pr["viewerCanUpdate"].(bool)
	return ports.SCMPRContent{
		Title:     str(pr["title"]),
		Body:      str(pr["body"]),
		UpdatedAt: parseGitHubTime(str(pr["updatedAt"])),
		CanEdit:   canEdit,
	}, nil
}

// EditPullRequest changes a pull request's title, description, or both through
// the REST pulls endpoint, and returns them as GitHub stored them.
func (p *Provider) EditPullRequest(ctx context.Context, request ports.SCMPREditRequest) (ports.SCMPRContent, error) {
	if p == nil || p.client == nil {
		return ports.SCMPRContent{}, fmt.Errorf("github scm: edit provider is not configured")
	}
	if err := validEditRef(request.PR); err != nil {
		return ports.SCMPRContent{}, err
	}
	if request.Title == nil && request.Body == nil {
		return ports.SCMPRContent{}, fmt.Errorf("github scm: nothing to edit")
	}
	if request.Title != nil && strings.TrimSpace(*request.Title) == "" {
		return ports.SCMPRContent{}, fmt.Errorf("github scm: pull request title is required")
	}
	// A nil field is left out, so GitHub keeps it; an empty body clears it.
	payload := struct {
		Title *string `json:"title,omitempty"`
		Body  *string `json:"body,omitempty"`
	}{Title: request.Title, Body: request.Body}
	resp, err := p.client.doREST(ctx, http.MethodPatch,
		repoPath(request.PR.Repo.Owner, request.PR.Repo.Name, "pulls", strconv.Itoa(request.PR.Number)), nil, payload)
	if err != nil {
		if resp.StatusCode == http.StatusForbidden {
			return ports.SCMPRContent{}, fmt.Errorf("%w: %w", ports.ErrSCMForbidden, err)
		}
		return ports.SCMPRContent{}, mapMutationStatus(resp.StatusCode, err)
	}
	var result struct {
		Title     string  `json:"title"`
		Body      *string `json:"body"`
		UpdatedAt string  `json:"updated_at"`
	}
	if err := json.Unmarshal(resp.Body, &result); err != nil {
		return ports.SCMPRContent{}, fmt.Errorf("github scm: decode pull request edit response: %w", err)
	}
	out := ports.SCMPRContent{Title: result.Title, UpdatedAt: parseGitHubTime(result.UpdatedAt), CanEdit: true}
	if result.Body != nil {
		out.Body = *result.Body
	}
	return out, nil
}

func validEditRef(ref ports.SCMPRRef) error {
	if ref.Number <= 0 || strings.TrimSpace(ref.Repo.Owner) == "" || strings.TrimSpace(ref.Repo.Name) == "" {
		return fmt.Errorf("github scm: invalid pull request reference")
	}
	return nil
}
