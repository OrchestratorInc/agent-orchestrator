package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.SCMPREditor = (*Provider)(nil)

// editableMR is the part of a merge request that an edit reads and writes.
type editableMR struct {
	IID         int        `json:"iid"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	UpdatedAt   *time.Time `json:"updated_at"`
}

func (mr editableMR) content() ports.SCMPRContent {
	return ports.SCMPRContent{Title: mr.Title, Body: mr.Description, UpdatedAt: safeTime(mr.UpdatedAt), CanEdit: true}
}

func editMRPath(ref ports.SCMPRRef) (string, error) {
	if ref.Number <= 0 || strings.TrimSpace(ref.Repo.Owner) == "" || strings.TrimSpace(ref.Repo.Name) == "" {
		return "", fmt.Errorf("gitlab scm: invalid merge request reference")
	}
	return fmt.Sprintf("/projects/%s/merge_requests/%d", projectPath(ref.Repo.Owner, ref.Repo.Name), ref.Number), nil
}

// PullRequestContent reads a merge request's title and description. GitLab's
// merge request does not say whether the signed-in account may edit it, so
// CanEdit is always true and GitLab refuses the edit itself.
func (p *Provider) PullRequestContent(ctx context.Context, ref ports.SCMPRRef) (ports.SCMPRContent, error) {
	if p == nil || p.client == nil {
		return ports.SCMPRContent{}, fmt.Errorf("gitlab scm: edit provider is not configured")
	}
	path, err := editMRPath(ref)
	if err != nil {
		return ports.SCMPRContent{}, err
	}
	client, err := p.clientForRepoErr(ref.Repo)
	if err != nil {
		return ports.SCMPRContent{}, err
	}
	// doGET revalidates its cached response with GitLab on every call, so this
	// is what GitLab holds now.
	resp, err := client.doGET(ctx, path, nil)
	if err != nil {
		return ports.SCMPRContent{}, err
	}
	var mr editableMR
	if err := json.Unmarshal(resp.Body, &mr); err != nil {
		return ports.SCMPRContent{}, fmt.Errorf("gitlab scm: decode merge request: %w", err)
	}
	return mr.content(), nil
}

// EditPullRequest changes a merge request's title, description, or both. A
// "Draft:" title prefix is how GitLab marks a draft, so a title edit can change
// that too, as it does on GitLab.
func (p *Provider) EditPullRequest(ctx context.Context, request ports.SCMPREditRequest) (ports.SCMPRContent, error) {
	if p == nil || p.client == nil {
		return ports.SCMPRContent{}, fmt.Errorf("gitlab scm: edit provider is not configured")
	}
	path, err := editMRPath(request.PR)
	if err != nil {
		return ports.SCMPRContent{}, err
	}
	if request.Title == nil && request.Body == nil {
		return ports.SCMPRContent{}, fmt.Errorf("gitlab scm: nothing to edit")
	}
	if request.Title != nil && strings.TrimSpace(*request.Title) == "" {
		return ports.SCMPRContent{}, fmt.Errorf("gitlab scm: merge request title is required")
	}
	client, err := p.clientForRepoErr(request.PR.Repo)
	if err != nil {
		return ports.SCMPRContent{}, err
	}
	resp, err := client.doMERGE(ctx, path, nil, struct {
		Title       *string `json:"title,omitempty"`
		Description *string `json:"description,omitempty"`
	}{Title: request.Title, Description: request.Body})
	if err != nil {
		if resp.StatusCode == http.StatusForbidden {
			return ports.SCMPRContent{}, fmt.Errorf("%w: %w", ports.ErrSCMForbidden, err)
		}
		return ports.SCMPRContent{}, mapMutationStatus(resp.StatusCode, err)
	}
	// The poll that follows an edit must not read the merge request from
	// before it and store the old title over the new one.
	if p.cache != nil {
		p.cache.dropMRDetail(request.PR.Repo.Host, request.PR.Repo.Repo, request.PR.Number)
	}
	var mr editableMR
	if err := json.Unmarshal(resp.Body, &mr); err != nil {
		return ports.SCMPRContent{}, fmt.Errorf("gitlab scm: decode merge request edit response: %w", err)
	}
	return mr.content(), nil
}
