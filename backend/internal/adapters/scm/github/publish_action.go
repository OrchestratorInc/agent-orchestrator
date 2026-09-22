package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.SCMPullRequestPublisher = (*Provider)(nil)

func (p *Provider) ReconcileOrCreatePullRequest(ctx context.Context, request ports.SCMPublishRequest) (ports.SCMPublishResult, error) {
	if p == nil || p.client == nil || strings.TrimSpace(request.Repo.Owner) == "" || strings.TrimSpace(request.Repo.Name) == "" || strings.TrimSpace(request.SourceBranch) == "" || strings.TrimSpace(request.TargetBranch) == "" || strings.TrimSpace(request.Title) == "" {
		return ports.SCMPublishResult{}, fmt.Errorf("github scm: invalid publish request")
	}
	prs, err := p.ListPRsByRepo(ctx, request.Repo, time.Time{})
	if err != nil {
		return ports.SCMPublishResult{}, err
	}
	for _, pr := range prs {
		if pr.SourceBranch == request.SourceBranch && pr.TargetBranch == request.TargetBranch && (pr.HeadRepo == "" || pr.HeadRepo == request.Repo.Repo) {
			return ports.SCMPublishResult{PR: pr, Reconciled: true}, nil
		}
	}
	payload := struct {
		Title string `json:"title"`
		Head  string `json:"head"`
		Base  string `json:"base"`
		Body  string `json:"body,omitempty"`
	}{Title: request.Title, Head: request.SourceBranch, Base: request.TargetBranch, Body: request.Body}
	resp, err := p.client.doREST(ctx, http.MethodPost, repoPath(request.Repo.Owner, request.Repo.Name, "pulls"), nil, payload)
	if err != nil {
		return ports.SCMPublishResult{}, err
	}
	var pull restListPull
	if err := json.Unmarshal(resp.Body, &pull); err != nil {
		return ports.SCMPublishResult{}, fmt.Errorf("github scm: decode created pull request: %w", err)
	}
	pr := restListPullToSCM(pull)
	if pr.Number <= 0 || pr.URL == "" {
		return ports.SCMPublishResult{}, fmt.Errorf("github scm: create response missing pull request identity")
	}
	return ports.SCMPublishResult{PR: pr}, nil
}
