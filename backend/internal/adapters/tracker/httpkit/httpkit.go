package httpkit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type RateLimitError struct {
	ResetAt    time.Time
	RetryAfter time.Duration
	Message    string
	Sentinel   error
}

func (e *RateLimitError) Error() string {
	if e == nil || e.Sentinel == nil {
		return "rate limited"
	}
	if e.Message != "" {
		return e.Sentinel.Error() + ": " + e.Message
	}
	return e.Sentinel.Error()
}

func (e *RateLimitError) Is(target error) bool {
	return e.Sentinel != nil && target == e.Sentinel
}

func BuildRateLimitError(resp *http.Response, msg string, sentinel error) *RateLimitError {
	e := &RateLimitError{Message: msg, Sentinel: sentinel}
	for _, h := range []string{"X-RateLimit-Reset", "RateLimit-Reset"} {
		if reset := resp.Header.Get(h); reset != "" {
			if sec, err := strconv.ParseInt(reset, 10, 64); err == nil && sec > 0 {
				e.ResetAt = time.Unix(sec, 0)
				break
			}
		}
	}
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if sec, err := strconv.Atoi(ra); err == nil && sec >= 0 {
			e.RetryAfter = time.Duration(sec) * time.Second
		}
	}
	return e
}

func ParseLinkNext(linkHeader, baseURL string) string {
	for _, part := range strings.Split(linkHeader, ",") {
		part = strings.TrimSpace(part)
		if part == "" || !strings.HasPrefix(part, "<") {
			continue
		}
		urlEnd := strings.Index(part, ">")
		if urlEnd < 0 {
			continue
		}
		rawURL := part[1:urlEnd]
		if !linkHasRelNext(part[urlEnd+1:]) {
			continue
		}
		return relativePathForLink(rawURL, baseURL)
	}
	return ""
}

func linkHasRelNext(params string) bool {
	for _, param := range strings.Split(params, ";") {
		name, value, ok := strings.Cut(strings.TrimSpace(param), "=")
		if !ok || !strings.EqualFold(name, "rel") {
			continue
		}
		value = strings.Trim(value, `"`)
		for _, rel := range strings.Fields(value) {
			if strings.EqualFold(rel, "next") {
				return true
			}
		}
	}
	return false
}

func relativePathForLink(rawLink, baseURL string) string {
	base, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	u, err := url.Parse(rawLink)
	if err != nil {
		return ""
	}
	if !u.IsAbs() {
		u = base.ResolveReference(u)
	}
	if u.Path == "" {
		return ""
	}
	path := u.EscapedPath()
	if base.Path != "" && strings.HasPrefix(path, base.Path) {
		path = strings.TrimPrefix(path, base.Path)
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	return path
}

func AppendIssuesWithLimit(dst, src []domain.Issue, limit int) ([]domain.Issue, bool) {
	if limit <= 0 {
		return append(dst, src...), false
	}
	remaining := limit - len(dst)
	if remaining <= 0 {
		return dst, true
	}
	if len(src) > remaining {
		return append(dst, src[:remaining]...), true
	}
	dst = append(dst, src...)
	return dst, len(dst) >= limit
}

func Message(body []byte) string {
	var p struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(body, &p) == nil {
		if p.Message != "" {
			return p.Message
		}
		return p.Error
	}
	return strings.TrimSpace(string(body))
}

type PreflightCache struct {
	ok atomic.Bool
	mu sync.Mutex
}

func (c *PreflightCache) Run(ctx context.Context, probe func(context.Context) error) error {
	if c.ok.Load() {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ok.Load() {
		return nil
	}
	if err := probe(ctx); err != nil {
		return err
	}
	c.ok.Store(true)
	return nil
}
