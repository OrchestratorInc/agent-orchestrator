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

var _ ports.SCMReviewSummaryPoster = (*Provider)(nil)

// PostReviewSummary posts a COMMENT review with a body and no inline comments.
// COMMENT is the only event GitHub accepts from the PR author's own account,
// and it leaves the PR's review decision untouched.
func (p *Provider) PostReviewSummary(ctx context.Context, request ports.SCMReviewSummaryRequest) (string, error) {
	if p == nil || p.client == nil {
		return "", fmt.Errorf("github scm: review summary poster is not configured")
	}
	if request.PR.Number <= 0 || strings.TrimSpace(request.PR.Repo.Owner) == "" || strings.TrimSpace(request.PR.Repo.Name) == "" {
		return "", fmt.Errorf("github scm: invalid pull request reference")
	}
	if strings.TrimSpace(request.Body) == "" {
		return "", fmt.Errorf("github scm: review summary body is required")
	}
	payload := struct {
		CommitID string `json:"commit_id,omitempty"`
		Body     string `json:"body"`
		Event    string `json:"event"`
	}{CommitID: strings.TrimSpace(request.CommitSHA), Body: request.Body, Event: "COMMENT"}
	resp, err := p.client.doREST(ctx, http.MethodPost,
		repoPath(request.PR.Repo.Owner, request.PR.Repo.Name, "pulls", strconv.Itoa(request.PR.Number), "reviews"),
		nil, payload)
	if err != nil {
		if resp.StatusCode == http.StatusNotFound {
			return "", fmt.Errorf("%w: %w", ports.ErrSCMNotFound, err)
		}
		return "", err
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(resp.Body, &created); err != nil {
		return "", fmt.Errorf("github scm: decode created review: %w", err)
	}
	if created.ID == 0 {
		return "", fmt.Errorf("github scm: created review has no id")
	}
	return strconv.FormatInt(created.ID, 10), nil
}
