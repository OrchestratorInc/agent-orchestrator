package accountsmanager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	managementClientTimeout = 5 * time.Second
	managementResponseLimit = 1 << 20
	managementAPIKeyLimit   = 16 << 10
	managementImportLimit   = 1 << 20
)

// ErrUnavailable and the other management errors exclude upstream response bodies.
var (
	ErrUnavailable             = errors.New("accounts manager unavailable")
	ErrResponseTooLarge        = errors.New("accounts manager response is too large")
	ErrRequestTooLarge         = errors.New("accounts manager request is too large")
	ErrInvalidResponse         = errors.New("accounts manager returned an invalid response")
	ErrUnsupportedProvider     = errors.New("accounts manager provider is unsupported")
	ErrCredentialNotFound      = errors.New("accounts manager credential was not found")
	ErrCredentialConflict      = errors.New("accounts manager credential is ambiguous or already exists")
	ErrInvalidCredential       = errors.New("accounts manager credential is invalid")
	ErrCredentialMethod        = errors.New("sign-in token is not an API key")
	ErrVerificationUnavailable = errors.New("accounts manager could not verify the credential")
	ErrOperationUnsupported    = errors.New("accounts manager operation is unsupported")
	ErrOAuthBusy               = errors.New("accounts manager OAuth login is already in progress")
	ErrOAuthExpired            = errors.New("accounts manager OAuth login expired")
)

// ManagementStatusError reports an upstream HTTP status without retaining or
// exposing the response body.
type ManagementStatusError struct {
	Operation  string
	StatusCode int
}

func (e *ManagementStatusError) Error() string {
	return fmt.Sprintf("accounts manager %s failed with status %d", e.Operation, e.StatusCode)
}

type managementTransportError struct {
	operation string
	cause     error
}

func (e *managementTransportError) Error() string {
	return "accounts manager " + e.operation + " request failed"
}

func (e *managementTransportError) Unwrap() error { return e.cause }

// Provider limits management operations to supported account providers.
type Provider string

// ProviderCodex and the other provider constants identify supported account types.
const (
	ProviderCodex  Provider = "codex"
	ProviderClaude Provider = "claude"
)

// CredentialKind distinguishes authentication formats without exposing their contents.
type CredentialKind string

// CredentialOAuth and the other credential kinds describe stored authentication formats.
const (
	CredentialOAuth       CredentialKind = "oauth"
	CredentialAPIKey      CredentialKind = "api_key"
	CredentialAccessToken CredentialKind = "access_token"
	CredentialUnknown     CredentialKind = "unknown"
)

// CredentialState records the runner's latest status, which may be unknown.
type CredentialState string

// CredentialActive and the other states are runner observations, not inferred health.
const (
	CredentialActive       CredentialState = "active"
	CredentialPending      CredentialState = "pending"
	CredentialRefreshing   CredentialState = "refreshing"
	CredentialError        CredentialState = "error"
	CredentialDisabled     CredentialState = "disabled"
	CredentialUnknownState CredentialState = "unknown"
)

// CredentialCooldown preserves provider-scoped retry timing for an account or model.
type CredentialCooldown struct {
	Scope            string    `json:"scope"`
	Model            string    `json:"model_key"`
	Reason           string    `json:"reason"`
	RetryAt          time.Time `json:"retry_at"`
	RemainingSeconds int64     `json:"remaining_seconds"`
	HTTPStatus       int       `json:"http_status"`
}

// CredentialQuotaObservation keeps observation time separate from cached quota signals.
type CredentialQuotaObservation struct {
	ObservedAt time.Time
	Signals    map[string]string
}

// CredentialSummary is a secret-free runner projection; Ref remains daemon-private.
type CredentialSummary struct {
	Verification       string
	VerifiedAt         time.Time
	Label              string
	Generation         uint64
	ReconnectSupported bool
	Ref                string
	Provider           Provider
	Kind               CredentialKind
	Email              string
	Status             CredentialState
	Disabled           bool
	Unavailable        bool
	ObservedAt         time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
	LastRefreshedAt    time.Time
	QuotaSupported     bool
	Cooldowns          []CredentialCooldown
	Quota              CredentialQuotaObservation
	ModelQuota         map[string]CredentialQuotaObservation
}

// RoutingStrategy describes the engine selector, independently of durable session pins.
type RoutingStrategy string

// RoutingRoundRobin and the other strategies are accepted engine selector values.
const (
	RoutingRoundRobin         RoutingStrategy = "round-robin"
	RoutingWeightedRoundRobin RoutingStrategy = "weighted-round-robin"
	RoutingFillFirst          RoutingStrategy = "fill-first"
)

// RouteCapability is private child-process connection material. It must never
// cross the daemon's public API boundary or be persisted.
type RouteCapability struct {
	BaseURL string
	Token   string
}

// EndpointSource exposes private transport material with a readiness flag.
type EndpointSource interface {
	Endpoint() (Endpoint, bool)
}

// ManagementClient bounds private runner requests and serializes credential edits.
type ManagementClient struct {
	source     EndpointSource
	client     *http.Client
	mutationMu sync.Mutex
}

// NewManagementClient disables redirects so management credentials cannot be forwarded.
func NewManagementClient(source EndpointSource, client *http.Client) *ManagementClient {
	if client == nil {
		client = &http.Client{}
	}
	bounded := *client
	if bounded.Timeout <= 0 || bounded.Timeout > managementClientTimeout {
		bounded.Timeout = managementClientTimeout
	}
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &ManagementClient{source: source, client: &bounded}
}

// ListCredentials filters runner records to supported providers without returning secrets.
func (c *ManagementClient) ListCredentials(ctx context.Context) ([]CredentialSummary, error) {
	var payload struct {
		ObservedAt time.Time             `json:"observed_at"`
		Files      []rawCredentialRecord `json:"files"`
	}
	if err := c.doJSON(ctx, "list credentials", http.MethodGet, credentialManagementPath, nil, &payload); err != nil {
		return nil, err
	}

	credentials := make([]CredentialSummary, 0, len(payload.Files))
	for _, file := range payload.Files {
		ref := strings.TrimSpace(file.AuthIndex)
		if ref == "" {
			continue
		}
		providerName := strings.ToLower(strings.TrimSpace(file.Provider))
		if providerName == "" {
			providerName = strings.ToLower(strings.TrimSpace(file.Type))
		}
		provider := Provider(providerName)
		if provider != ProviderCodex && provider != ProviderClaude {
			continue
		}
		summary := summaryFromRawCredential(file)
		summary.ObservedAt = payload.ObservedAt
		credentials = append(credentials, summary)
	}
	return credentials, nil
}

// RoutingStrategy returns only recognized selector values from the runner.
func (c *ManagementClient) RoutingStrategy(ctx context.Context) (RoutingStrategy, error) {
	var payload struct {
		Strategy string `json:"strategy"`
	}
	if err := c.doJSON(ctx, "read routing strategy", http.MethodGet, "/v0/management/routing/strategy", nil, &payload); err != nil {
		return "", err
	}
	strategy := RoutingStrategy(strings.TrimSpace(payload.Strategy))
	switch strategy {
	case RoutingRoundRobin, RoutingWeightedRoundRobin, RoutingFillFirst:
		return strategy, nil
	default:
		return "", ErrInvalidResponse
	}
}

// MintRoute returns child-only material after verifying the runner's loopback origin.
func (c *ManagementClient) MintRoute(ctx context.Context, provider Provider, ref, sessionID, accountID string, bindingRevision int64) (RouteCapability, error) {
	if provider != ProviderCodex && provider != ProviderClaude {
		return RouteCapability{}, ErrUnsupportedProvider
	}
	ref = strings.TrimSpace(ref)
	sessionID = strings.TrimSpace(sessionID)
	if ref == "" || sessionID == "" || accountID == "" || bindingRevision <= 0 {
		return RouteCapability{}, ErrInvalidCredential
	}
	var payload struct {
		BaseURL string `json:"baseUrl"`
		Token   string `json:"token"`
	}
	err := c.doJSON(ctx, "mint route", http.MethodPost, "/ao/internal/routes/token", map[string]any{
		"provider": string(provider), "authIndex": ref, "sessionId": sessionID, "accountId": accountID, "bindingRevision": bindingRevision,
	}, &payload)
	if err != nil {
		var statusErr *ManagementStatusError
		if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusNotFound {
			return RouteCapability{}, ErrCredentialNotFound
		}
		return RouteCapability{}, err
	}
	endpoint, ready := c.source.Endpoint()
	baseURL, ok := verifiedManagementBaseURL(payload.BaseURL)
	expected, expectedOK := verifiedManagementBaseURL(endpoint.BaseURL)
	if !ready || !ok || !expectedOK || baseURL != expected || strings.TrimSpace(payload.Token) == "" {
		return RouteCapability{}, ErrInvalidResponse
	}
	return RouteCapability{BaseURL: baseURL, Token: payload.Token}, nil
}

func (c *ManagementClient) doJSON(ctx context.Context, operation, method, path string, src, dst any) error {
	if c == nil || c.source == nil || c.client == nil {
		return ErrUnavailable
	}
	endpoint, ready := c.source.Endpoint()
	if !ready || strings.TrimSpace(endpoint.ManagementToken) == "" {
		return ErrUnavailable
	}
	baseURL, ok := verifiedManagementBaseURL(endpoint.BaseURL)
	if !ok {
		return ErrUnavailable
	}

	var requestBody []byte
	var err error
	if src != nil {
		requestBody, err = json.Marshal(src)
		if err != nil {
			return ErrInvalidResponse
		}
		if len(requestBody) > managementImportLimit {
			return ErrRequestTooLarge
		}
	}
	defer clear(requestBody)
	var bodyReader io.Reader
	if requestBody != nil {
		bodyReader = bytes.NewReader(requestBody)
	}
	req, err := http.NewRequestWithContext(ctx, method, baseURL+path, bodyReader)
	if err != nil {
		return ErrUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+endpoint.ManagementToken)
	req.Header.Set("Accept", "application/json")
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.client.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return &managementTransportError{operation: operation, cause: err}
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return &ManagementStatusError{Operation: operation, StatusCode: res.StatusCode}
	}
	if dst == nil || res.StatusCode == http.StatusNoContent {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, managementResponseLimit+1))
	if err != nil {
		return &managementTransportError{operation: operation, cause: err}
	}
	if len(body) > managementResponseLimit {
		return ErrResponseTooLarge
	}
	mediaType, _, parseErr := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if parseErr != nil || mediaType != "application/json" {
		return ErrInvalidResponse
	}
	if err = json.Unmarshal(body, dst); err != nil {
		return ErrInvalidResponse
	}
	return nil
}

func verifiedManagementBaseURL(raw string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", false
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", false
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 {
		return "", false
	}
	return "http://127.0.0.1:" + strconv.Itoa(port), true
}
