package session

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const issueContextBodyLimit = 12000

func (s *Service) withIssueContext(ctx context.Context, cfg ports.SpawnConfig, project domain.ProjectRecord) ports.SpawnConfig {
	if cfg.IssueContext != "" || cfg.IssueID == "" || s.tracker == nil {
		return cfg
	}
	if cfg.Kind != "" && cfg.Kind != domain.KindWorker {
		return cfg
	}
	id, ok := s.trackerIDForIssue(cfg, project)
	if !ok {
		return cfg
	}
	issue, err := s.tracker.Get(ctx, id)
	if err != nil {
		return cfg
	}
	if issueContext := formatIssueContext(issue); issueContext != "" {
		cfg.IssueContext = issueContext
	}
	return cfg
}

func (s *Service) trackerIDForIssue(cfg ports.SpawnConfig, project domain.ProjectRecord) (domain.TrackerID, bool) {
	issue := strings.TrimPrefix(strings.TrimSpace(string(cfg.IssueID)), "#")
	if issue == "" {
		return domain.TrackerID{}, false
	}
	if native, ok := canonicalGitHubIssueNative(issue); ok {
		return domain.TrackerID{Provider: domain.TrackerProviderGitHub, Native: native}, true
	}
	if native, host, ok := canonicalGitLabIssueURL(issue); ok {
		return domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: native, Host: host}, true
	}
	n, err := strconv.Atoi(issue)
	if err != nil || n <= 0 {
		return domain.TrackerID{}, false
	}
	provider, host, repo, ok := s.repoForTracker(project, cfg.TrackerProvider)
	if !ok {
		return domain.TrackerID{}, false
	}
	return domain.TrackerID{Provider: provider, Native: fmt.Sprintf("%s#%d", repo, n), Host: host}, true
}

func (s *Service) repoForTracker(project domain.ProjectRecord, fallbackProvider domain.TrackerProvider) (domain.TrackerProvider, string, string, bool) {
	if s.scm != nil {
		repo, ok := s.scm.ParseRepository(project.RepoOriginURL)
		if ok && repo.Provider != "" && repo.Repo != "" {
			return domain.TrackerProvider(repo.Provider), normalizeTrackerHost(repo.Provider, repo.Host), repo.Repo, true
		}
	}
	host, owner, repo, err := repoFromURL(project.RepoOriginURL)
	if err != nil {
		return "", "", "", false
	}
	provider := fallbackProvider
	if provider == "" {
		provider = domain.TrackerProviderGitHub
	}
	return provider, normalizeTrackerHost(string(provider), host), owner + "/" + repo, true
}

func normalizeTrackerHost(provider, host string) string {
	if provider != string(domain.TrackerProviderGitLab) {
		return ""
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "gitlab.com" || host == "www.gitlab.com" {
		return ""
	}
	return host
}

func canonicalGitHubIssueNative(raw string) (string, bool) {
	if strings.Contains(raw, "://") {
		return canonicalGitHubIssueURL(raw)
	}
	hash := strings.LastIndexByte(raw, '#')
	if hash <= 0 || hash == len(raw)-1 {
		return "", false
	}
	repo := strings.Trim(raw[:hash], "/")
	owner, name, ok := splitIssueOwnerRepo(repo)
	if !ok {
		return "", false
	}
	n, err := strconv.Atoi(raw[hash+1:])
	if err != nil || n <= 0 {
		return "", false
	}
	return fmt.Sprintf("%s/%s#%d", owner, name, n), true
}

func splitIssueOwnerRepo(repo string) (string, string, bool) {
	parts := strings.Split(strings.Trim(repo, "/"), "/")
	if len(parts) != 2 {
		return "", "", false
	}
	owner := strings.TrimSpace(parts[0])
	name := strings.TrimSuffix(strings.TrimSpace(parts[1]), ".git")
	return owner, name, owner != "" && name != ""
}

func canonicalGitHubIssueURL(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Hostname(), "github.com") {
		return "", false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 4 || parts[2] != "issues" {
		return "", false
	}
	n, err := strconv.Atoi(parts[3])
	if err != nil || n <= 0 {
		return "", false
	}
	return fmt.Sprintf("%s/%s#%d", parts[0], strings.TrimSuffix(parts[1], ".git"), n), true
}

func canonicalGitLabIssueURL(raw string) (native, host string, ok bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", "", false
	}
	path := strings.Trim(u.Path, "/")
	if path == "" {
		return "", "", false
	}
	idx := strings.Index(path, "/-/issues/")
	if idx <= 0 {
		return "", "", false
	}
	projectPath := path[:idx]
	rest := path[idx+len("/-/issues/"):]
	if rest == "" {
		return "", "", false
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n <= 0 {
		return "", "", false
	}
	parts := strings.Split(projectPath, "/")
	for _, p := range parts {
		if p == "" {
			return "", "", false
		}
	}
	if len(parts) < 2 {
		return "", "", false
	}
	parts[len(parts)-1] = strings.TrimSuffix(parts[len(parts)-1], ".git")
	host = u.Host
	if strings.EqualFold(host, "gitlab.com") || strings.EqualFold(host, "www.gitlab.com") {
		host = ""
	}
	return fmt.Sprintf("%s#%d", strings.Join(parts, "/"), n), host, true
}

func formatIssueContext(issue domain.Issue) string {
	var b strings.Builder
	writeIssueLine(&b, "Issue", issue.ID.Native)
	writeIssueLine(&b, "Title", issue.Title)
	writeIssueLine(&b, "State", string(issue.State))
	writeIssueLine(&b, "URL", issue.URL)
	if len(issue.Labels) > 0 {
		writeIssueLine(&b, "Labels", strings.Join(issue.Labels, ", "))
	}
	if len(issue.Assignees) > 0 {
		writeIssueLine(&b, "Assignees", strings.Join(issue.Assignees, ", "))
	}
	body := strings.TrimSpace(domain.SanitizeControlChars(issue.Body))
	if body != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("Body:\n")
		b.WriteString(truncateIssueBody(body, issueContextBodyLimit))
	}
	return strings.TrimSpace(b.String())
}

func writeIssueLine(b *strings.Builder, label, value string) {
	value = strings.TrimSpace(domain.SanitizeControlChars(value))
	if value == "" {
		return
	}
	if b.Len() > 0 {
		b.WriteByte('\n')
	}
	fmt.Fprintf(b, "%s: %s", label, value)
}

func truncateIssueBody(body string, limit int) string {
	runes := []rune(body)
	if limit <= 0 || len(runes) <= limit {
		return body
	}
	return string(runes[:limit]) + fmt.Sprintf("\n\n[Issue body truncated to %d characters.]", limit)
}
