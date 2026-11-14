package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var (
	ErrNotFound    = ports.ErrSCMNotFound
	ErrRateLimited = fmt.Errorf("gitlab scm: rate limited")
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
		return "gitlab scm: rate limited: " + e.Message
	}
	return ErrRateLimited.Error()
}

func (e *RateLimitError) Is(target error) bool { return target == ErrRateLimited }

func (e *RateLimitError) GetRetryAfter() time.Duration {
	if e == nil {
		return 0
	}
	return e.RetryAfter
}

func (e *RateLimitError) GetResetAt() time.Time {
	if e == nil {
		return time.Time{}
	}
	return e.ResetAt
}

const (
	defaultRESTBaseURL = "https://gitlab.com/api/v4"
	cacheMaxEntries    = 512
	defaultUserAgent   = "ao-gitlab-scm/1"
	defaultHTTPTimeout = 30 * time.Second
	jobLogTailMaxBytes = 65536
	errorBodyMaxBytes  = 4096
)

type RESTResponse struct {
	StatusCode  int
	NotModified bool
	ETag        string
	Body        []byte
}

type ClientOptions struct {
	HTTPClient *http.Client
	Token      TokenSource
	RESTBase   string
	UserAgent  string
}

type Client struct {
	http      *http.Client
	tokens    TokenSource
	restBase  string
	userAgent string

	mu       sync.Mutex
	etagOut  map[string]string
	bodyOut  map[string][]byte
	cacheLRU []string
}

func NewClient(opts ClientOptions) *Client {
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: defaultHTTPTimeout}
	}
	base := opts.RESTBase
	if base == "" {
		base = defaultRESTBaseURL
	}
	base = strings.TrimRight(base, "/")
	ua := opts.UserAgent
	if ua == "" {
		ua = defaultUserAgent
	}
	return &Client{
		http:      hc,
		tokens:    opts.Token,
		restBase:  base,
		userAgent: ua,
		etagOut:   make(map[string]string, cacheMaxEntries),
		bodyOut:   make(map[string][]byte, cacheMaxEntries),
	}
}

func (c *Client) doRESTWithETag(ctx context.Context, path string, q url.Values, etag string) (RESTResponse, error) {
	u := c.restURL(path, q)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return RESTResponse{}, err
	}
	if err := c.authorize(ctx, req); err != nil {
		return RESTResponse{}, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return RESTResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := readResponseBody(resp, path)
	if err != nil {
		return RESTResponse{StatusCode: resp.StatusCode}, err
	}
	if resp.StatusCode == http.StatusNotModified {
		return RESTResponse{StatusCode: 304, NotModified: true, ETag: etag}, nil
	}
	if resp.StatusCode >= 400 {
		return RESTResponse{StatusCode: resp.StatusCode}, classifyError(resp, body)
	}
	return RESTResponse{
		StatusCode: resp.StatusCode,
		ETag:       resp.Header.Get("ETag"),
		Body:       body,
	}, nil
}

func (c *Client) doMERGE(ctx context.Context, path string, q url.Values, body any) (RESTResponse, error) {
	return c.doWrite(ctx, http.MethodPut, path, q, body)
}

func (c *Client) doPOST(ctx context.Context, path string, q url.Values, body any) (RESTResponse, error) {
	return c.doWrite(ctx, http.MethodPost, path, q, body)
}

func (c *Client) doWrite(ctx context.Context, method, path string, q url.Values, body any) (RESTResponse, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return RESTResponse{}, fmt.Errorf("gitlab scm: encode %s body: %w", path, err)
		}
		rdr = bytes.NewReader(b)
	}

	u := c.restURL(path, q)
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return RESTResponse{}, fmt.Errorf("gitlab scm: build %s request: %w", path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", c.userAgent)
	if err := c.authorize(ctx, req); err != nil {
		return RESTResponse{}, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return RESTResponse{}, fmt.Errorf("gitlab scm: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := readResponseBody(resp, path)
	if err != nil {
		return RESTResponse{StatusCode: resp.StatusCode}, err
	}
	if resp.StatusCode >= 400 {
		return RESTResponse{StatusCode: resp.StatusCode, Body: b}, classifyError(resp, b)
	}
	return RESTResponse{StatusCode: resp.StatusCode, Body: b}, nil
}

func (c *Client) doPOSTFile(ctx context.Context, path, fileName, contentType string, data []byte) (RESTResponse, error) {
	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": "file", "filename": fileName}))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	header.Set("Content-Type", contentType)
	part, err := form.CreatePart(header)
	if err != nil {
		return RESTResponse{}, fmt.Errorf("gitlab scm: build %s form: %w", path, err)
	}
	if _, err := part.Write(data); err != nil {
		return RESTResponse{}, fmt.Errorf("gitlab scm: build %s form: %w", path, err)
	}
	if err := form.Close(); err != nil {
		return RESTResponse{}, fmt.Errorf("gitlab scm: build %s form: %w", path, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.restURL(path, nil), &buf)
	if err != nil {
		return RESTResponse{}, fmt.Errorf("gitlab scm: build %s request: %w", path, err)
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.Header.Set("User-Agent", c.userAgent)
	if err := c.authorize(ctx, req); err != nil {
		return RESTResponse{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return RESTResponse{}, fmt.Errorf("gitlab scm: POST %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := readResponseBody(resp, path)
	if err != nil {
		return RESTResponse{StatusCode: resp.StatusCode}, err
	}
	if resp.StatusCode >= 400 {
		return RESTResponse{StatusCode: resp.StatusCode, Body: b}, classifyError(resp, b)
	}
	return RESTResponse{StatusCode: resp.StatusCode, Body: b}, nil
}

func (c *Client) doGET(ctx context.Context, path string, q url.Values) (RESTResponse, error) {
	u := c.restURL(path, q)
	cacheKey := u
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return RESTResponse{}, err
	}
	if err := c.authorize(ctx, req); err != nil {
		return RESTResponse{}, err
	}
	req.Header.Set("User-Agent", c.userAgent)

	c.mu.Lock()
	prevETag := c.etagOut[cacheKey]
	prevBody := c.bodyOut[cacheKey]
	c.mu.Unlock()
	if prevETag != "" && prevBody != nil {
		req.Header.Set("If-None-Match", prevETag)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return RESTResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := readResponseBody(resp, path)
	if err != nil {
		return RESTResponse{StatusCode: resp.StatusCode}, err
	}

	if resp.StatusCode == http.StatusNotModified {
		if prevETag == "" || prevBody == nil {
			return RESTResponse{StatusCode: resp.StatusCode}, fmt.Errorf("gitlab scm: GET %s returned 304 without a cached body", path)
		}
		etag := resp.Header.Get("ETag")
		if etag == "" {
			etag = prevETag
		}
		return RESTResponse{StatusCode: 200, NotModified: true, ETag: etag, Body: bytes.Clone(prevBody)}, nil
	}
	if resp.StatusCode >= 400 {
		return RESTResponse{StatusCode: resp.StatusCode}, classifyError(resp, body)
	}
	if etag := resp.Header.Get("ETag"); etag != "" {
		c.storeCacheEntry(cacheKey, etag, body)
	}
	return RESTResponse{
		StatusCode: resp.StatusCode,
		ETag:       resp.Header.Get("ETag"),
		Body:       body,
	}, nil
}

func (c *Client) doGETRaw(ctx context.Context, path string) ([]byte, error) {
	u := c.restURL(path, nil)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, err
	}
	if err := c.authorize(ctx, req); err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyMaxBytes))
		return nil, classifyError(resp, body)
	}
	return readTail(resp.Body, jobLogTailMaxBytes)
}

func readTail(r io.Reader, maxBytes int) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, nil
	}
	buf := make([]byte, 0, maxBytes)
	chunk := make([]byte, 32*1024)
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
			if len(buf) > maxBytes {
				buf = append(buf[:0:0], buf[len(buf)-maxBytes:]...)
			}
		}
		if err == io.EOF {
			return buf, nil
		}
		if err != nil {
			return buf, err
		}
	}
}

const maxPaginationPages = 10

func (c *Client) doGETPaginated(ctx context.Context, path string, q url.Values, handler func(body []byte) error) (bool, error) {
	if q == nil {
		q = url.Values{}
	}
	if q.Get("per_page") == "" {
		q.Set("per_page", "100")
	}
	nextURL := c.restURL(path, q)
	for page := 0; page < maxPaginationPages; page++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, nextURL, http.NoBody)
		if err != nil {
			return false, err
		}
		if err := c.authorize(ctx, req); err != nil {
			return false, err
		}
		req.Header.Set("User-Agent", c.userAgent)
		resp, err := c.http.Do(req)
		if err != nil {
			return false, err
		}
		body, err := readResponseBody(resp, path)
		_ = resp.Body.Close()
		if err != nil {
			return false, err
		}
		if resp.StatusCode == http.StatusNotModified {
			return false, nil
		}
		if resp.StatusCode >= 400 {
			return false, classifyError(resp, body)
		}
		if err := handler(body); err != nil {
			return false, err
		}
		next := parseNextLink(resp.Header.Get("Link"))
		if next == "" {
			return false, nil
		}
		if err := c.validatePaginationURL(next); err != nil {
			return false, err
		}
		nextURL = next
	}
	return true, nil
}

func parseNextLink(linkHeader string) string {
	if linkHeader == "" {
		return ""
	}
	for _, part := range strings.Split(linkHeader, ",") {
		part = strings.TrimSpace(part)
		if !strings.Contains(part, `rel="next"`) {
			continue
		}
		lt := strings.Index(part, "<")
		gt := strings.Index(part, ">")
		if lt < 0 || gt < 0 || gt <= lt {
			continue
		}
		return part[lt+1 : gt]
	}
	return ""
}

var ErrPaginationURLRejected = fmt.Errorf("gitlab scm: pagination URL rejected")

func (c *Client) validatePaginationURL(next string) error {
	nextURL, err := url.Parse(next)
	if err != nil {
		return fmt.Errorf("gitlab scm: parse pagination URL: %w", ErrPaginationURLRejected)
	}
	if nextURL.Host == "" {
		return nil
	}
	base, err := url.Parse(c.restBase)
	if err != nil {
		return fmt.Errorf("gitlab scm: parse REST base: %w", ErrPaginationURLRejected)
	}
	if base.Scheme == "https" && nextURL.Scheme == "http" {
		return fmt.Errorf("gitlab scm: pagination URL downgrades https to http: %w", ErrPaginationURLRejected)
	}
	if !strings.EqualFold(nextURL.Scheme, base.Scheme) {
		return fmt.Errorf("gitlab scm: pagination URL scheme %q != base %q: %w", nextURL.Scheme, base.Scheme, ErrPaginationURLRejected)
	}
	if !strings.EqualFold(nextURL.Hostname(), base.Hostname()) {
		return fmt.Errorf("gitlab scm: pagination URL host %q != base %q: %w", nextURL.Hostname(), base.Hostname(), ErrPaginationURLRejected)
	}
	if nextURL.Port() != base.Port() {
		return fmt.Errorf("gitlab scm: pagination URL port %q != base %q: %w", nextURL.Port(), base.Port(), ErrPaginationURLRejected)
	}
	if !strings.HasPrefix(nextURL.Path, base.Path) {
		return fmt.Errorf("gitlab scm: pagination URL path %q does not start with API base %q: %w", nextURL.Path, base.Path, ErrPaginationURLRejected)
	}
	return nil
}

func (c *Client) storeCacheEntry(cacheKey, etag string, body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.etagOut[cacheKey] = etag
	c.bodyOut[cacheKey] = bytes.Clone(body)
	c.cacheLRU = append(c.cacheLRU, cacheKey)
	for len(c.cacheLRU) > cacheMaxEntries {
		evict := c.cacheLRU[0]
		c.cacheLRU = c.cacheLRU[1:]
		delete(c.etagOut, evict)
		delete(c.bodyOut, evict)
	}
}

func readResponseBody(resp *http.Response, path string) ([]byte, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		err = fmt.Errorf("gitlab scm: read %s body: %w", path, err)
		if resp.StatusCode >= 400 {
			err = errors.Join(err, classifyError(resp, body))
		}
		return nil, err
	}
	return body, nil
}

func (c *Client) authorize(ctx context.Context, req *http.Request) error {
	if c.tokens == nil {
		return nil
	}
	tok, err := c.tokens.Token(ctx)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	return nil
}

func (c *Client) restURL(path string, q url.Values) string {
	u := c.restBase + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

func classifyError(resp *http.Response, body []byte) error {
	switch resp.StatusCode {
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrAuthFailed
	case http.StatusTooManyRequests:
		return gitlabRateLimited(resp, body)
	default:
		msg := gitlabMessage(body)
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("gitlab scm: %s", msg)
	}
}

func gitlabRateLimited(resp *http.Response, body []byte) error {
	e := &RateLimitError{Message: gitlabMessage(body)}
	if reset := resp.Header.Get("RateLimit-Reset"); reset != "" {
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

func gitlabMessage(body []byte) string {
	var v struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(body, &v) == nil {
		if v.Message != "" {
			return v.Message
		}
		return v.Error
	}
	return ""
}

func projectPath(owner, name string) string {
	return url.PathEscape(owner + "/" + name)
}
