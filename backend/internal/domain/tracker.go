package domain

import (
	"fmt"
	"net/url"
	"strings"
)

type TrackerProvider string

const (
	TrackerProviderGitHub TrackerProvider = "github"
	TrackerProviderGitLab TrackerProvider = "gitlab"
)

type TrackerID struct {
	Provider TrackerProvider `json:"provider"`
	Native   string          `json:"native"`
	Host     string          `json:"host,omitempty"`
}

type NormalizedIssueState string

const (
	IssueOpen       NormalizedIssueState = "open"
	IssueInProgress NormalizedIssueState = "in_progress"
	IssueInReview   NormalizedIssueState = "review"
	IssueDone       NormalizedIssueState = "done"
	IssueCancelled  NormalizedIssueState = "cancelled"
)

type Issue struct {
	ID        TrackerID            `json:"id"`
	Title     string               `json:"title"`
	Body      string               `json:"body"`
	State     NormalizedIssueState `json:"state"`
	URL       string               `json:"url"`
	Labels    []string             `json:"labels,omitempty"`
	Assignees []string             `json:"assignees,omitempty"`
}

type TrackerRepo struct {
	Provider TrackerProvider `json:"provider"`
	Native   string          `json:"native"`
	Host     string          `json:"host,omitempty"`
}

type ListStateFilter string

const (
	ListAll    ListStateFilter = ""
	ListOpen   ListStateFilter = "open"
	ListClosed ListStateFilter = "closed"
)

type ListFilter struct {
	State    ListStateFilter `json:"state,omitempty"`
	Labels   []string        `json:"labels,omitempty"`
	Assignee string          `json:"assignee,omitempty"`
	Limit    int             `json:"limit,omitempty"`
}

type TrackerIntakeConfig struct {
	Enabled  bool            `json:"enabled,omitempty"`
	Provider TrackerProvider `json:"provider,omitempty" enum:"github,gitlab"`
	Repo     string          `json:"repo,omitempty"`
	Assignee string          `json:"assignee,omitempty"`
}

func (c TrackerIntakeConfig) WithDefaults() TrackerIntakeConfig {
	return c
}

func InferTrackerProvider(repoURL string) TrackerProvider {
	raw := strings.TrimSpace(repoURL)
	if raw == "" {
		return TrackerProviderGitHub
	}
	if strings.HasPrefix(raw, "git@") {
		if _, rest, ok := strings.Cut(raw, "@"); ok {
			if host, path, ok := strings.Cut(rest, ":"); ok {
				raw = "https://" + host + "/" + path
			}
		}
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return TrackerProviderGitHub
	}
	host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
	if host == "github.com" || strings.HasSuffix(host, ".github.com") || strings.HasSuffix(host, ".ghe.io") {
		return TrackerProviderGitHub
	}
	return TrackerProviderGitLab
}

func (c TrackerIntakeConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.Provider != "" && c.Provider != TrackerProviderGitHub && c.Provider != TrackerProviderGitLab {
		return fmt.Errorf("trackerIntake.provider: unsupported provider %q", c.Provider)
	}
	if err := validateNoWhitespaceField("trackerIntake.repo", c.Repo); err != nil {
		return err
	}
	if err := validateNoWhitespaceField("trackerIntake.assignee", c.Assignee); err != nil {
		return err
	}
	if strings.TrimSpace(c.Assignee) == "" {
		return fmt.Errorf("trackerIntake: assignee is required when enabled")
	}
	return nil
}
