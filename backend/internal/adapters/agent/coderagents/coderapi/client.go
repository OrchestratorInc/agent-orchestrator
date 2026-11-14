package coderapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"

	"strings"
	"time"
)

// SessionTokenHeader is Coder's authentication header. It is a custom header,
// so Go's redirect handling would forward it to another origin; the client's
// redirect policy prevents that.
const SessionTokenHeader = "Coder-Session-Token"

const maxResponseBytes = 8 << 20

// ErrCrossOriginRedirect is returned when the deployment redirects a request
// to a different scheme, host or port. Following it would send the session
// token to that origin.
var ErrCrossOriginRedirect = errors.New("coder deployment redirected to another origin; refusing to forward the session token")

// APIError is a non-success response. Message and Detail come from Coder's
// standard error body and never contain request headers.
type APIError struct {
	StatusCode int
	Message    string
	Detail     string
}

func (e *APIError) Error() string {
	text := strings.TrimSpace(e.Message)
	if text == "" {
		text = http.StatusText(e.StatusCode)
	}
	if detail := strings.TrimSpace(e.Detail); detail != "" {
		text += ": " + detail
	}
	return fmt.Sprintf("coder API %d: %s", e.StatusCode, text)
}

// StatusCode returns the HTTP status of an APIError in err's chain, or 0.
func StatusCode(err error) int {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode
	}
	return 0
}

// Client talks to one Coder deployment with one session token.
type Client struct {
	base  *url.URL
	token string
	http  *http.Client
}

// NewClient builds a client whose HTTP requests never leave the
// deployment's origin. transport may be nil for the default transport.
func NewClient(creds Credentials, transport http.RoundTripper) *Client {
	if transport == nil {
		transport = http.DefaultTransport
	}
	base := *creds.URL
	return &Client{
		base: &base, token: creds.Token,
		http: &http.Client{
			Transport:     transport,
			CheckRedirect: sameOriginRedirects(&base),
		},
	}
}

func sameOriginRedirects(base *url.URL) func(*http.Request, []*http.Request) error {
	origin := originOf(base)
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("coder deployment redirected too many times")
		}
		target := *req.URL
		switch target.Scheme {
		case "ws":
			target.Scheme = "http"
		case "wss":
			target.Scheme = "https"
		}
		if originOf(&target) != origin {
			return ErrCrossOriginRedirect
		}
		return nil
	}
}

// originOf normalizes scheme, host and the effective port.
func originOf(u *url.URL) string {
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port == "" {
		switch scheme {
		case "https", "wss":
			port = "443"
		default:
			port = "80"
		}
	}
	switch scheme {
	case "ws":
		scheme = "http"
	case "wss":
		scheme = "https"
	}
	return scheme + "://" + net.JoinHostPort(host, port)
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base.JoinPath(path).String(), http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set(SessionTokenHeader, c.token)
	req.Header.Set("Accept", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		if errors.Is(err, ErrCrossOriginRedirect) {
			return ErrCrossOriginRedirect
		}
		// url.Error carries the request URL; it holds no secret because the
		// token travels only in a header.
		return fmt.Errorf("coder GET %s: %w", path, err)
	}
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("read coder response: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		return decodeAPIError(res.StatusCode, data)
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode coder GET %s response: %w", path, err)
	}
	return nil
}

func decodeAPIError(status int, data []byte) error {
	var body struct {
		Message string `json:"message"`
		Detail  string `json:"detail"`
	}
	_ = json.Unmarshal(data, &body)
	return &APIError{StatusCode: status, Message: body.Message, Detail: body.Detail}
}

// Me returns the signed-in user. A 401 means the session is invalid.
func (c *Client) Me(ctx context.Context) (User, error) {
	var user User
	err := c.get(ctx, "/api/v2/users/me", &user)
	return user, err
}

// Organizations lists the signed-in user's organizations.
func (c *Client) Organizations(ctx context.Context) ([]Organization, error) {
	var orgs []Organization
	err := c.get(ctx, "/api/v2/users/me/organizations", &orgs)
	return orgs, err
}

// ChatModels lists the organization's chat model configs visible to the user.
// A 404 means the deployment has no stable Coder Agents API.
func (c *Client) ChatModels(ctx context.Context, organizationID string) ([]ChatModel, error) {
	var res struct {
		Models []ChatModel `json:"models"`
	}
	err := c.get(ctx, "/api/v2/organizations/"+url.PathEscape(organizationID)+"/chats/models", &res)
	return res.Models, err
}

// requestTimeout bounds one REST round trip.
const requestTimeout = 30 * time.Second

// WithRequestTimeout returns ctx bounded for one REST call.
func WithRequestTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, requestTimeout)
}
