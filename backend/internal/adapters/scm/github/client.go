package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	defaultRESTBaseURL = "https://api.github.com"
	defaultGraphQLURL  = "https://api.github.com/graphql"
	defaultUserAgent   = "ao-agent-orchestrator/scm-github"
)

var (
	ErrNotFound    = ports.ErrSCMNotFound
	ErrAuthFailed  = errors.New("github scm: authentication failed")
	ErrRateLimited = errors.New("github scm: rate limited")
	errGraphQL     = errors.New("github scm: graphql error")
)

type RateLimitError struct {
	ResetAt    time.Time
	RetryAfter time.Duration
	Message    string
}

func (e *RateLimitError) Error() string {
	if e == nil {
		return ErrRateLimited.Error()
	}
	if e.Message != "" {
		return "github scm: rate limited: " + e.Message
	}
	return ErrRateLimited.Error()
}

func (e *RateLimitError) Is(target error) bool { return target == ErrRateLimited }

type ClientOptions struct {
	HTTPClient *http.Client
	Token      TokenSource
	RESTBase   string
	GraphQLURL string
	UserAgent  string
}

type Client struct {
	http       *http.Client
	tokens     TokenSource
	restBase   string
	graphqlURL string
	userAgent  string

	mu       sync.Mutex
	etagOut  map[string]string
	bodyOut  map[string][]byte
	cacheLRU []string
}

const cacheMaxEntries = 512

func NewClient(opts ClientOptions) *Client {
	c := &Client{
		http:       opts.HTTPClient,
		tokens:     opts.Token,
		restBase:   opts.RESTBase,
		graphqlURL: opts.GraphQLURL,
		userAgent:  opts.UserAgent,
		etagOut:    map[string]string{},
		bodyOut:    map[string][]byte{},
	}
	if c.http == nil {
		c.http = &http.Client{Timeout: 30 * time.Second}
	}
	if c.restBase == "" {
		c.restBase = defaultRESTBaseURL
	}
	if c.graphqlURL == "" {
		c.graphqlURL = defaultGraphQLURL
	}
	if c.userAgent == "" {
		c.userAgent = defaultUserAgent
	}
	return c
}

type RESTResponse struct {
	StatusCode  int
	NotModified bool
	ETag        string
	Body        []byte
}

func (c *Client) doRESTWithETag(ctx context.Context, path string, q url.Values, etag string) (RESTResponse, error) {
	u, err := c.restURL(path, q)
	if err != nil {
		return RESTResponse{}, fmt.Errorf("github scm: build %s URL: %w", path, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return RESTResponse{}, fmt.Errorf("github scm: build GET %s request: %w", path, err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", c.userAgent)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if err := c.authorize(ctx, req); err != nil {
		return RESTResponse{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return RESTResponse{}, fmt.Errorf("github scm: GET %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotModified {
		return RESTResponse{StatusCode: resp.StatusCode, NotModified: true, ETag: firstNonEmptyHeader(resp.Header.Get("ETag"), etag)}, nil
	}
	b, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return RESTResponse{}, fmt.Errorf("github scm: read %s body: %w", path, readErr)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return RESTResponse{StatusCode: resp.StatusCode, ETag: resp.Header.Get("ETag"), Body: b}, nil
	}
	err = classifyError(resp, b)
	if errors.Is(err, ErrAuthFailed) {
		c.invalidateToken()
	}
	return RESTResponse{StatusCode: resp.StatusCode, Body: b}, err
}

func (c *Client) doREST(ctx context.Context, method, path string, q url.Values, body any) (RESTResponse, error) {
	cacheable := method == http.MethodGet
	cacheKey := method + " " + path + "?" + q.Encode()
	var prevETag string
	var prevBody []byte
	if cacheable {
		c.mu.Lock()
		prevETag = c.etagOut[cacheKey]
		prevBody = c.bodyOut[cacheKey]
		c.mu.Unlock()
	}

	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return RESTResponse{}, fmt.Errorf("github scm: encode %s %s body: %w", method, path, err)
		}
		rdr = bytes.NewReader(b)
	}

	u, err := c.restURL(path, q)
	if err != nil {
		return RESTResponse{}, fmt.Errorf("github scm: build %s URL: %w", path, err)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return RESTResponse{}, fmt.Errorf("github scm: build %s %s request: %w", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", c.userAgent)
	if prevETag != "" {
		req.Header.Set("If-None-Match", prevETag)
	}
	if err := c.authorize(ctx, req); err != nil {
		return RESTResponse{}, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return RESTResponse{}, fmt.Errorf("github scm: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if cacheable && resp.StatusCode == http.StatusNotModified {
		newETag := resp.Header.Get("ETag")
		if newETag != "" && newETag != prevETag {
			c.mu.Lock()
			c.etagOut[cacheKey] = newETag
			c.mu.Unlock()
		}
		return RESTResponse{StatusCode: resp.StatusCode, NotModified: true, ETag: newETag, Body: prevBody}, nil
	}

	b, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return RESTResponse{}, fmt.Errorf("github scm: read %s body: %w", path, readErr)
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		etag := resp.Header.Get("ETag")
		if cacheable && etag != "" {
			c.storeCacheEntry(cacheKey, etag, append([]byte(nil), b...))
		}
		return RESTResponse{StatusCode: resp.StatusCode, ETag: etag, Body: b}, nil
	}

	err = classifyError(resp, b)
	if errors.Is(err, ErrAuthFailed) {
		c.invalidateToken()
	}
	return RESTResponse{StatusCode: resp.StatusCode, Body: b}, err
}

type graphQLError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Path    []any  `json:"path"`
}

func (c *Client) doGraphQL(ctx context.Context, query string, variables map[string]any) (map[string]any, error) {
	payload := map[string]any{"query": query}
	if variables != nil {
		payload["variables"] = variables
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("github scm: encode graphql body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.graphqlURL, bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("github scm: build graphql request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if err := c.authorize(ctx, req); err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github scm: POST graphql: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, fmt.Errorf("github scm: read graphql body: %w", readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := classifyError(resp, respBody)
		if errors.Is(err, ErrAuthFailed) {
			c.invalidateToken()
		}
		return nil, err
	}
	var decoded struct {
		Data   map[string]any `json:"data"`
		Errors []graphQLError `json:"errors"`
	}
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return nil, fmt.Errorf("github scm: decode graphql response: %w", err)
	}
	decoded.Errors = slices.DeleteFunc(decoded.Errors, func(e graphQLError) bool {
		return slices.Contains(e.Path, any("workflowRun"))
	})
	if len(decoded.Errors) > 0 {
		msg := decoded.Errors[0].Message
		low := strings.ToLower(msg)
		switch {
		case strings.Contains(low, "rate limit") || strings.Contains(low, "abuse"):
			return decoded.Data, &RateLimitError{Message: msg}
		case strings.Contains(low, "bad credentials") || strings.Contains(low, "credentials"):
			c.invalidateToken()
			return decoded.Data, fmt.Errorf("%w: %s", ErrAuthFailed, msg)
		case strings.Contains(low, "could not resolve") || strings.Contains(low, "not found"):
			return decoded.Data, fmt.Errorf("%w: %s", ErrNotFound, msg)
		default:
			return decoded.Data, fmt.Errorf("%w: %s", errGraphQL, msg)
		}
	}
	return decoded.Data, nil
}

func (c *Client) fetchPlainText(ctx context.Context, path string) ([]byte, error) {
	u, err := c.restURL(path, nil)
	if err != nil {
		return nil, fmt.Errorf("github scm: build %s URL: %w", path, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("github scm: build %s request: %w", path, err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", c.userAgent)
	if err := c.authorize(ctx, req); err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github scm: GET %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, fmt.Errorf("github scm: read %s body: %w", path, readErr)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return body, nil
	}
	return nil, classifyError(resp, body)
}

func (c *Client) storeCacheEntry(cacheKey, etag string, body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.etagOut[cacheKey]; !exists {
		c.cacheLRU = append(c.cacheLRU, cacheKey)
	}
	c.etagOut[cacheKey] = etag
	c.bodyOut[cacheKey] = body
	for len(c.cacheLRU) > cacheMaxEntries {
		evict := c.cacheLRU[0]
		c.cacheLRU = c.cacheLRU[1:]
		delete(c.etagOut, evict)
		delete(c.bodyOut, evict)
	}
}

func (c *Client) authorize(ctx context.Context, req *http.Request) error {
	if c.tokens == nil {
		return nil
	}
	token, err := c.tokens.Token(ctx)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrAuthFailed, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return nil
}

func (c *Client) invalidateToken() {
	if inv, ok := c.tokens.(tokenInvalidator); ok {
		inv.InvalidateToken()
	}
}

func (c *Client) restURL(path string, q url.Values) (string, error) {
	base, err := url.Parse(c.restBase)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	base.Path = strings.TrimSuffix(base.Path, "/") + path
	if q != nil {
		base.RawQuery = q.Encode()
	}
	return base.String(), nil
}

func classifyError(resp *http.Response, body []byte) error {
	msg := githubMessage(body)
	switch resp.StatusCode {
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrNotFound, msg)
	case http.StatusTooManyRequests:
		return rateLimited(resp, msg)
	case http.StatusUnauthorized:
		return fmt.Errorf("%w: %s", ErrAuthFailed, msg)
	case http.StatusForbidden:
		if isRateLimited(resp, msg) {
			return rateLimited(resp, msg)
		}
		return fmt.Errorf("%w: %s", ErrAuthFailed, msg)
	}
	return fmt.Errorf("github scm: %d %s", resp.StatusCode, msg)
}

func isRateLimited(resp *http.Response, msg string) bool {
	if rem := resp.Header.Get("X-RateLimit-Remaining"); rem != "" {
		if n, err := strconv.Atoi(rem); err == nil && n == 0 {
			return true
		}
	}
	if resp.Header.Get("Retry-After") != "" {
		return true
	}
	low := strings.ToLower(msg)
	return strings.Contains(low, "rate limit") || strings.Contains(low, "abuse detection") || strings.Contains(low, "secondary rate")
}

func rateLimited(resp *http.Response, msg string) error {
	e := &RateLimitError{Message: msg}
	if reset := resp.Header.Get("X-RateLimit-Reset"); reset != "" {
		if sec, err := strconv.ParseInt(reset, 10, 64); err == nil && sec > 0 {
			e.ResetAt = time.Unix(sec, 0)
		}
	}
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if sec, err := strconv.Atoi(ra); err == nil && sec >= 0 {
			e.RetryAfter = time.Duration(sec) * time.Second
		}
	}
	return e
}

func githubMessage(body []byte) string {
	var p struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &p) == nil && p.Message != "" {
		return p.Message
	}
	return strings.TrimSpace(string(body))
}

func firstNonEmptyHeader(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
