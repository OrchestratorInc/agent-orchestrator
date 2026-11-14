package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
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
	out := ports.SCMHeadChecks{Since: p.firstObservedHead(repo, sha)}
	resp, err := p.client.doREST(ctx, http.MethodGet, repoPath(repo.Owner, repo.Name, "commits", sha, "check-suites"), url.Values{"per_page": {"100"}}, nil)
	if err != nil {
		return ports.SCMHeadChecks{}, fmt.Errorf("github scm: read check suites: %w", err)
	}
	var suites struct {
		TotalCount  int `json:"total_count"`
		CheckSuites []struct {
			Status               string `json:"status"`
			Conclusion           string `json:"conclusion"`
			LatestCheckRunsCount int    `json:"latest_check_runs_count"`
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

// Commit and suite dates can predate a push or lie in the future. Only our
// observation of this repository/head starts the no-checks grace period.
func (p *Provider) firstObservedHead(repo ports.SCMRepo, sha string) time.Time {
	p.headObservedMu.Lock()
	defer p.headObservedMu.Unlock()
	key := strings.ToLower(repo.Owner+"/"+repo.Name) + "/" + sha
	if since, ok := p.headObserved[key]; ok {
		return since
	}
	if p.headObserved == nil {
		p.headObserved = make(map[string]time.Time)
	}
	// Bound history in a daemon that can observe new heads for weeks.
	if len(p.headObserved) >= 1024 {
		var oldest string
		for k, t := range p.headObserved {
			if oldest == "" || t.Before(p.headObserved[oldest]) {
				oldest = k
			}
		}
		delete(p.headObserved, oldest)
	}
	now := time.Now()
	if p.now != nil {
		now = p.now()
	}
	p.headObserved[key] = now
	return now
}
