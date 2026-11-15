package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	scmgitlab "github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/gitlab"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/tracker/httpkit"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	defaultBaseURL   = "https://gitlab.com/api/v4"
	defaultUserAgent = "ao-agent-orchestrator/tracker-gitlab"

	listPageSize = 100
	maxListPages = 50
)

var (
	ErrNotFound       = errors.New("gitlab tracker: issue not found")
	ErrRateLimited    = errors.New("gitlab tracker: rate limited")
	ErrAuthFailed     = errors.New("gitlab tracker: authentication failed")
	ErrWrongProvider  = errors.New("gitlab tracker: id is not a gitlab tracker id")
	ErrBadID          = errors.New("gitlab tracker: malformed native id")
	ErrHostNotAllowed = errors.New("gitlab tracker: host not in allowlist")
)

type RateLimitError = httpkit.RateLimitError

type Options struct {
	Token        scmgitlab.TokenSource
	HTTPClient   *http.Client
	BaseURL      string
	UserAgent    string
	AllowedHosts []string
	HostTokens   map[string]scmgitlab.TokenSource
}

type hostEntry struct {
	baseURL string
	tokens  scmgitlab.TokenSource
}

type Tracker struct {
	http      *http.Client
	userAgent string

	defaultHost hostEntry

	hosts map[string]hostEntry

	preflight httpkit.PreflightCache
}

func New(opts Options) (*Tracker, error) {
	src := opts.Token
	if src == nil {
		return nil, ErrNoToken
	}
	if _, err := src.Token(context.Background()); err != nil {
		return nil, err
	}
	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	ua := opts.UserAgent
	if ua == "" {
		ua = defaultUserAgent
	}

	hosts := make(map[string]hostEntry, len(opts.AllowedHosts))
	for _, raw := range opts.AllowedHosts {
		h := scmgitlab.NormalizeHost(raw)
		if h == "" || scmgitlab.IsGitLabDotCom(h) {
			continue
		}
		he := hostEntry{
			baseURL: "https://" + h + "/api/v4",
			tokens:  src,
		}
		if ts, ok := opts.HostTokens[h]; ok && ts != nil {
			he.tokens = ts
		}
		hosts[h] = he
	}

	t := &Tracker{
		http:        opts.HTTPClient,
		userAgent:   ua,
		defaultHost: hostEntry{baseURL: baseURL, tokens: src},
		hosts:       hosts,
	}
	if t.http == nil {
		t.http = &http.Client{Timeout: 30 * time.Second}
	}
	return t, nil
}

func (t *Tracker) configForHost(host string) (hostEntry, error) {
	host = scmgitlab.NormalizeHost(host)
	if scmgitlab.IsGitLabDotCom(host) {
		return t.defaultHost, nil
	}
	if he, ok := t.hosts[host]; ok {
		return he, nil
	}
	return hostEntry{}, fmt.Errorf("gitlab tracker: host %q not in allowlist: %w", host, ErrHostNotAllowed)
}

func (t *Tracker) ConfigForHost(host string) error {
	_, err := t.configForHost(host)
	return err
}

var _ ports.Tracker = (*Tracker)(nil)

type glIssue struct {
	IID         int      `json:"iid"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	State       string   `json:"state"`
	WebURL      string   `json:"web_url"`
	Labels      []string `json:"labels"`
	Assignees   []glUser `json:"assignees"`
}

type glUser struct {
	Username string `json:"username"`
}

func (t *Tracker) Get(ctx context.Context, id domain.TrackerID) (domain.Issue, error) {
	projectPath, iid, err := t.parseID(id)
	if err != nil {
		return domain.Issue{}, err
	}
	he, err := t.configForHost(id.Host)
	if err != nil {
		return domain.Issue{}, err
	}
	path := fmt.Sprintf("/projects/%s/issues/%d", url.PathEscape(projectPath), iid)

	resp, err := t.do(ctx, he, http.MethodGet, path, nil)
	if err != nil {
		return domain.Issue{}, err
	}
	var raw glIssue
	if err := json.Unmarshal(resp, &raw); err != nil {
		return domain.Issue{}, fmt.Errorf("gitlab tracker: decode issue: %w", err)
	}
	issue := issueFromGitLab(projectPath, raw)
	issue.ID.Host = id.Host
	return issue, nil
}

func issueFromGitLab(projectPath string, raw glIssue) domain.Issue {
	assignees := make([]string, 0, len(raw.Assignees))
	for _, a := range raw.Assignees {
		assignees = append(assignees, a.Username)
	}
	out := domain.Issue{
		ID: domain.TrackerID{
			Provider: domain.TrackerProviderGitLab,
			Native:   fmt.Sprintf("%s#%d", projectPath, raw.IID),
		},
		Title:     raw.Title,
		Body:      raw.Description,
		State:     mapStateFromGitLab(raw.State),
		URL:       raw.WebURL,
		Labels:    raw.Labels,
		Assignees: assignees,
	}
	if len(out.Labels) == 0 {
		out.Labels = nil
	}
	if len(out.Assignees) == 0 {
		out.Assignees = nil
	}
	return out
}

func mapStateFromGitLab(state string) domain.NormalizedIssueState {
	if strings.EqualFold(state, "closed") {
		return domain.IssueDone
	}
	return domain.IssueOpen
}

func (t *Tracker) List(ctx context.Context, repo domain.TrackerRepo, filter domain.ListFilter) ([]domain.Issue, error) {
	if repo.Provider != domain.TrackerProviderGitLab {
		return nil, fmt.Errorf("%w: provider=%q", ErrWrongProvider, repo.Provider)
	}
	projectPath, err := parseGitLabRepo(repo.Native)
	if err != nil {
		return nil, err
	}
	he, err := t.configForHost(repo.Host)
	if err != nil {
		return nil, err
	}

	q := url.Values{}
	switch filter.State {
	case domain.ListOpen:
		q.Set("state", "opened")
	case domain.ListClosed:
		q.Set("state", "closed")
	default:
		q.Set("state", "all")
	}
	if len(filter.Labels) > 0 {
		q.Set("labels", strings.Join(filter.Labels, ","))
	}
	if filter.Assignee != "" {
		q.Set("assignee_username", filter.Assignee)
	}
	q.Set("per_page", strconv.Itoa(listPageSize))

	path := fmt.Sprintf("/projects/%s/issues?%s", url.PathEscape(projectPath), q.Encode())
	out := make([]domain.Issue, 0)
	if filter.Limit > 0 {
		out = make([]domain.Issue, 0, filter.Limit)
	}
	for page := 0; path != ""; page++ {
		if page >= maxListPages {
			return nil, fmt.Errorf("gitlab tracker: list pagination exceeded %d pages", maxListPages)
		}
		respBody, nextPath, err := t.roundTrip(ctx, he, http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		var raw []glIssue
		if err := json.Unmarshal(respBody, &raw); err != nil {
			return nil, fmt.Errorf("gitlab tracker: decode list: %w", err)
		}
		pageIssues := make([]domain.Issue, 0, len(raw))
		for _, r := range raw {
			issue := issueFromGitLab(projectPath, r)
			issue.ID.Host = repo.Host
			pageIssues = append(pageIssues, issue)
		}
		var done bool
		out, done = httpkit.AppendIssuesWithLimit(out, pageIssues, filter.Limit)
		if done {
			break
		}
		path = nextPath
	}
	return out, nil
}

func (t *Tracker) Preflight(ctx context.Context) error {
	return t.preflight.Run(ctx, func(ctx context.Context) error {
		_, err := t.do(ctx, t.defaultHost, http.MethodGet, "/user", nil)
		return err
	})
}

func (t *Tracker) do(ctx context.Context, he hostEntry, method, path string, body any) ([]byte, error) {
	respBody, _, err := t.roundTrip(ctx, he, method, path, body)
	return respBody, err
}

func (t *Tracker) roundTrip(ctx context.Context, he hostEntry, method, path string, body any) ([]byte, string, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, "", fmt.Errorf("gitlab tracker: encode body: %w", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, he.baseURL+path, rdr)
	if err != nil {
		return nil, "", fmt.Errorf("gitlab tracker: build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", t.userAgent)
	tok, err := he.tokens.Token(ctx)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := t.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("gitlab tracker: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	nextPath := httpkit.ParseLinkNext(resp.Header.Get("Link"), he.baseURL)
	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, "", fmt.Errorf("gitlab tracker: read response body: %w", readErr)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return respBody, nextPath, nil
	}
	return respBody, nextPath, classifyError(resp, respBody)
}

func classifyError(resp *http.Response, body []byte) error {
	msg := httpkit.Message(body)
	switch resp.StatusCode {
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrNotFound, msg)
	case http.StatusTooManyRequests:
		return httpkit.BuildRateLimitError(resp, msg, ErrRateLimited)
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w: %s", ErrAuthFailed, msg)
	}
	return fmt.Errorf("gitlab tracker: %d %s", resp.StatusCode, msg)
}

func (t *Tracker) parseID(id domain.TrackerID) (projectPath string, iid int, err error) {
	if id.Provider != domain.TrackerProviderGitLab {
		return "", 0, fmt.Errorf("%w: provider=%q", ErrWrongProvider, id.Provider)
	}
	return parseGitLabID(id.Native)
}

func parseGitLabID(native string) (projectPath string, iid int, err error) {
	hash := strings.IndexByte(native, '#')
	if hash < 0 {
		return "", 0, fmt.Errorf("%w: missing #iid", ErrBadID)
	}
	projectPath = native[:hash]
	iidStr := native[hash+1:]
	if err := validateProjectPath(projectPath); err != nil {
		return "", 0, err
	}
	n, parseErr := strconv.Atoi(iidStr)
	if parseErr != nil || n <= 0 {
		return "", 0, fmt.Errorf("%w: bad iid %q", ErrBadID, iidStr)
	}
	return projectPath, n, nil
}

func parseGitLabRepo(native string) (string, error) {
	if native == "" {
		return "", fmt.Errorf("%w: empty repo", ErrBadID)
	}
	if err := validateProjectPath(native); err != nil {
		return "", err
	}
	return native, nil
}

func validateProjectPath(path string) error {
	if path == "" {
		return fmt.Errorf("%w: empty project path", ErrBadID)
	}
	if !strings.Contains(path, "/") {
		return fmt.Errorf("%w: missing project path separator", ErrBadID)
	}
	if strings.ContainsAny(path, " \t\n\r#") {
		return fmt.Errorf("%w: invalid project path %q", ErrBadID, path)
	}
	return nil
}
