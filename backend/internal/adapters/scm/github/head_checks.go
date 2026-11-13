package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// HeadChecks reads what GitHub knows about the checks on sha: its check
// suites, its commit statuses, and when checks could first have started. An
// app installed on the repository that never runs here (Vercel, Cursor, a
// review bot) still creates a suite for every push and leaves it queued with
// no check runs, so a queued suite only means "not started", not "checking".
func (p *Provider) HeadChecks(ctx context.Context, repo ports.SCMRepo, sha string) (ports.SCMHeadChecks, error) {
	if p == nil || p.client == nil {
		return ports.SCMHeadChecks{}, fmt.Errorf("github scm: head checks provider is not configured")
	}
	var out ports.SCMHeadChecks
	resp, err := p.client.doREST(ctx, http.MethodGet, repoPath(repo.Owner, repo.Name, "commits", sha, "check-suites"), url.Values{"per_page": {"100"}}, nil)
	if err != nil {
		return ports.SCMHeadChecks{}, fmt.Errorf("github scm: read check suites: %w", err)
	}
	var suites struct {
		TotalCount  int `json:"total_count"`
		CheckSuites []struct {
			Status               string    `json:"status"`
			Conclusion           string    `json:"conclusion"`
			LatestCheckRunsCount int       `json:"latest_check_runs_count"`
			CreatedAt            time.Time `json:"created_at"`
		} `json:"check_suites"`
	}
	if err := json.Unmarshal(resp.Body, &suites); err != nil {
		return ports.SCMHeadChecks{}, fmt.Errorf("github scm: decode check suites: %w", err)
	}
	out.Suites = suites.TotalCount
	for _, suite := range suites.CheckSuites {
		if !idleSuite(suite.Status, suite.Conclusion, suite.LatestCheckRunsCount) {
			out.Started++
		}
		if !suite.CreatedAt.IsZero() && (out.Since.IsZero() || suite.CreatedAt.Before(out.Since)) {
			out.Since = suite.CreatedAt
		}
	}
	// Suites past the first page went unread: count them as started.
	out.Started += max(0, suites.TotalCount-len(suites.CheckSuites))

	resp, err = p.client.doREST(ctx, http.MethodGet, repoPath(repo.Owner, repo.Name, "commits", sha, "status"), nil, nil)
	if err != nil {
		return ports.SCMHeadChecks{}, fmt.Errorf("github scm: read commit statuses: %w", err)
	}
	var status struct {
		TotalCount int `json:"total_count"`
	}
	if err := json.Unmarshal(resp.Body, &status); err != nil {
		return ports.SCMHeadChecks{}, fmt.Errorf("github scm: decode commit statuses: %w", err)
	}
	out.Statuses = status.TotalCount

	if out.Since.IsZero() {
		resp, err = p.client.doREST(ctx, http.MethodGet, repoPath(repo.Owner, repo.Name, "commits", sha), nil, nil)
		if err != nil {
			return ports.SCMHeadChecks{}, fmt.Errorf("github scm: read commit: %w", err)
		}
		var commit struct {
			Commit struct {
				Committer struct {
					Date time.Time `json:"date"`
				} `json:"committer"`
			} `json:"commit"`
		}
		if err := json.Unmarshal(resp.Body, &commit); err != nil {
			return ports.SCMHeadChecks{}, fmt.Errorf("github scm: decode commit: %w", err)
		}
		out.Since = commit.Commit.Committer.Date
	}
	return out, nil
}

// idleSuite is a suite that checked nothing: queued with no runs (an app that
// hasn't started, or never will), or finished as skipped or stale without one.
// Anything else, a run in progress or a suite waiting for approval included,
// counts as checking.
func idleSuite(status, conclusion string, runs int) bool {
	if runs > 0 {
		return false
	}
	switch status {
	case "queued":
		return true
	case "completed":
		return conclusion == "skipped" || conclusion == "stale"
	}
	return false
}
