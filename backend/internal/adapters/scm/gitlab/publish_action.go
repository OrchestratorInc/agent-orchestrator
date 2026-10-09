package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.SCMPullRequestPublisher = (*Provider)(nil)

// ReconcileOrCreatePullRequest returns an existing matching merge request or creates one.
func (p *Provider) ReconcileOrCreatePullRequest(ctx context.Context, request ports.SCMPublishRequest) (ports.SCMPublishResult, error) {
	if p == nil || strings.TrimSpace(request.Repo.Owner) == "" || strings.TrimSpace(request.Repo.Name) == "" || strings.TrimSpace(request.SourceBranch) == "" || strings.TrimSpace(request.TargetBranch) == "" || strings.TrimSpace(request.Title) == "" {
		return ports.SCMPublishResult{}, fmt.Errorf("gitlab scm: invalid publish request")
	}
	prs, err := p.ListPRsByRepo(ctx, request.Repo, time.Time{})
	if err != nil {
		return ports.SCMPublishResult{}, err
	}
	for _, pr := range prs {
		if !pr.Closed && !pr.Merged && pr.SourceBranch == request.SourceBranch && pr.TargetBranch == request.TargetBranch && (pr.HeadRepo == "" || pr.HeadRepo == request.Repo.Repo) {
			return ports.SCMPublishResult{PR: pr, Reconciled: true}, nil
		}
	}
	client, err := p.clientForRepoErr(request.Repo)
	if err != nil {
		return ports.SCMPublishResult{}, err
	}
	path := fmt.Sprintf("/projects/%s/merge_requests", projectPath(request.Repo.Owner, request.Repo.Name))
	payload := map[string]string{"source_branch": request.SourceBranch, "target_branch": request.TargetBranch, "title": request.Title, "description": request.Body}
	resp, err := client.doPOST(ctx, path, url.Values{}, payload)
	if err != nil {
		return ports.SCMPublishResult{}, err
	}
	var mr restMR
	if err := json.Unmarshal(resp.Body, &mr); err != nil {
		return ports.SCMPublishResult{}, fmt.Errorf("gitlab scm: decode created merge request: %w", err)
	}
	pr := mrToSCMPRObservation(request.Repo, &mr)
	if pr.Number <= 0 || pr.URL == "" {
		return ports.SCMPublishResult{}, fmt.Errorf("gitlab scm: create response missing merge request identity")
	}
	return ports.SCMPublishResult{PR: pr}, nil
}
