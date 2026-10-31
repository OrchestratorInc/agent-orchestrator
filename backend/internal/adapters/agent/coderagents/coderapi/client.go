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
	"strconv"
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

// NewClient builds a client whose HTTP and WebSocket requests never leave the
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

// BaseURL returns a copy of the deployment URL (no secrets).
func (c *Client) BaseURL() *url.URL {
	u := *c.base
	return &u
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

func (c *Client) endpoint(path string, query url.Values) *url.URL {
	u := c.base.JoinPath(path)
	if len(query) > 0 {
		u.RawQuery = query.Encode()
	}
	return u
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any, want ...int) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode coder request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint(path, query).String(), reader)
	if err != nil {
		return err
	}
	req.Header.Set(SessionTokenHeader, c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		if errors.Is(err, ErrCrossOriginRedirect) {
			return ErrCrossOriginRedirect
		}
		// url.Error carries the request URL; it holds no secret because the
		// token travels only in a header.
		return fmt.Errorf("coder %s %s: %w", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("read coder response: %w", err)
	}
	ok := false
	for _, code := range want {
		ok = ok || res.StatusCode == code
	}
	if !ok {
		return decodeAPIError(res.StatusCode, data)
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode coder %s %s response: %w", method, path, err)
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

// BuildInfo reads the deployment version.
func (c *Client) BuildInfo(ctx context.Context) (BuildInfo, error) {
	var info BuildInfo
	err := c.do(ctx, http.MethodGet, "/api/v2/buildinfo", nil, nil, &info, http.StatusOK)
	return info, err
}

// Me returns the signed-in user. A 401 means the session is invalid.
func (c *Client) Me(ctx context.Context) (User, error) {
	var user User
	err := c.do(ctx, http.MethodGet, "/api/v2/users/me", nil, nil, &user, http.StatusOK)
	return user, err
}

// Organizations lists the signed-in user's organizations.
func (c *Client) Organizations(ctx context.Context) ([]Organization, error) {
	var orgs []Organization
	err := c.do(ctx, http.MethodGet, "/api/v2/users/me/organizations", nil, nil, &orgs, http.StatusOK)
	return orgs, err
}

// ChatModels lists the organization's chat model configs visible to the user.
// A 404 means the deployment has no stable Coder Agents API.
func (c *Client) ChatModels(ctx context.Context, organizationID string) ([]ChatModel, error) {
	var res struct {
		Models []ChatModel `json:"models"`
	}
	err := c.do(ctx, http.MethodGet, "/api/v2/organizations/"+url.PathEscape(organizationID)+"/chats/models", nil, nil, &res, http.StatusOK)
	return res.Models, err
}

// CreateChat creates a chat with its first user message.
func (c *Client) CreateChat(ctx context.Context, req CreateChatRequest) (Chat, error) {
	var chat Chat
	err := c.do(ctx, http.MethodPost, "/api/v2/chats", nil, req, &chat, http.StatusCreated)
	return chat, err
}

// GetChat reads one chat.
func (c *Client) GetChat(ctx context.Context, chatID string) (Chat, error) {
	var chat Chat
	err := c.do(ctx, http.MethodGet, "/api/v2/chats/"+url.PathEscape(chatID), nil, nil, &chat, http.StatusOK)
	return chat, err
}

// ListChatsByLabels lists the user's chats carrying every given label,
// including archived ones so a lookup can never miss an existing chat.
func (c *Client) ListChatsByLabels(ctx context.Context, labels map[string]string) ([]Chat, error) {
	query := url.Values{}
	for key, value := range labels {
		query.Add("label", key+":"+value)
	}
	var chats []Chat
	err := c.do(ctx, http.MethodGet, "/api/v2/chats", query, nil, &chats, http.StatusOK)
	return chats, err
}

// CreateChatMessage sends a user message to an existing chat.
func (c *Client) CreateChatMessage(ctx context.Context, chatID string, req CreateChatMessageRequest) (CreateChatMessageResponse, error) {
	var res CreateChatMessageResponse
	err := c.do(ctx, http.MethodPost, "/api/v2/chats/"+url.PathEscape(chatID)+"/messages", nil, req, &res, http.StatusOK)
	return res, err
}

// ChatMessages returns messages after afterID (0 for all).
func (c *Client) ChatMessages(ctx context.Context, chatID string, afterID int64) (ChatMessagesResponse, error) {
	query := url.Values{}
	if afterID > 0 {
		query.Set("after_id", strconv.FormatInt(afterID, 10))
	}
	var res ChatMessagesResponse
	err := c.do(ctx, http.MethodGet, "/api/v2/chats/"+url.PathEscape(chatID)+"/messages", query, nil, &res, http.StatusOK)
	return res, err
}

// InterruptChat cancels the chat's in-flight run.
func (c *Client) InterruptChat(ctx context.Context, chatID string) (Chat, error) {
	var chat Chat
	err := c.do(ctx, http.MethodPost, "/api/v2/chats/"+url.PathEscape(chatID)+"/interrupt", nil, nil, &chat, http.StatusOK)
	return chat, err
}

// SubmitToolResults answers the chat's pending client-executed tool calls.
func (c *Client) SubmitToolResults(ctx context.Context, chatID string, results []ToolResult) error {
	return c.do(ctx, http.MethodPost, "/api/v2/chats/"+url.PathEscape(chatID)+"/tool-results", nil,
		SubmitToolResultsRequest{Results: results}, nil, http.StatusNoContent)
}

// requestTimeout bounds one REST round trip; streams have no deadline.
const requestTimeout = 30 * time.Second

// WithRequestTimeout returns ctx bounded for one REST call.
func WithRequestTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, requestTimeout)
}
