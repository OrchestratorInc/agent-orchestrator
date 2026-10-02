package accountsmanager

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OAuthEvent contains validated login progress without provider tokens.
type OAuthEvent struct {
	TargetRef        string
	Provider         Provider
	Mode             OAuthMode
	State            string
	Status           OAuthState
	AuthorizationURL string
	UserCode         string
	FailureCode      string
	ExpiresAt        time.Time
}

// StreamOAuthEvents blocks until the authenticated runner stream closes or
// ctx is cancelled. Heartbeats are ignored and only validated safe events are
// passed to consume.
func (c *ManagementClient) StreamOAuthEvents(ctx context.Context, consume func(OAuthEvent) error) error {
	if c == nil || c.source == nil || c.client == nil || consume == nil {
		return ErrUnavailable
	}
	endpoint, ready := c.source.Endpoint()
	baseURL, ok := verifiedManagementBaseURL(endpoint.BaseURL)
	if !ready || !ok || strings.TrimSpace(endpoint.ManagementToken) == "" {
		return ErrUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/ao/internal/oauth/events", http.NoBody)
	if err != nil {
		return ErrUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+endpoint.ManagementToken)
	req.Header.Set("Accept", "text/event-stream")
	streamClient := *c.client
	streamClient.Timeout = 0
	res, err := streamClient.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return &managementTransportError{operation: "stream OAuth events", cause: err}
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return &ManagementStatusError{Operation: "stream OAuth events", StatusCode: res.StatusCode}
	}
	mediaType, _, parseErr := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if parseErr != nil || mediaType != "text/event-stream" {
		return ErrInvalidResponse
	}

	scanner := bufio.NewScanner(res.Body)
	scanner.Buffer(make([]byte, 4096), managementResponseLimit)
	eventType := ""
	var data strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			if eventType == "oauth_session" && data.Len() > 0 {
				event, decodeErr := decodeOAuthEvent([]byte(data.String()))
				if decodeErr != nil {
					return decodeErr
				}
				if err := consume(event); err != nil {
					return err
				}
			}
			eventType = ""
			data.Reset()
		case strings.HasPrefix(line, "event:"):
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			part := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data.Len()+len(part)+1 > managementResponseLimit {
				return ErrResponseTooLarge
			}
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(part)
		}
	}
	if err = scanner.Err(); err != nil {
		return &managementTransportError{operation: "stream OAuth events", cause: err}
	}
	return nil
}

func decodeOAuthEvent(data []byte) (OAuthEvent, error) {
	var raw struct {
		TargetRef        string    `json:"targetRef"`
		Provider         string    `json:"provider"`
		Mode             string    `json:"mode"`
		State            string    `json:"state"`
		Status           string    `json:"status"`
		AuthorizationURL string    `json:"authorizationUrl"`
		UserCode         string    `json:"userCode"`
		FailureCode      string    `json:"failureCode"`
		ExpiresAt        time.Time `json:"expiresAt"`
	}
	if json.Unmarshal(data, &raw) != nil {
		return OAuthEvent{}, ErrInvalidResponse
	}
	event := OAuthEvent{Provider: Provider(strings.TrimSpace(raw.Provider)), Mode: OAuthMode(strings.TrimSpace(raw.Mode)), State: strings.TrimSpace(raw.State), Status: OAuthState(strings.TrimSpace(raw.Status)), AuthorizationURL: strings.TrimSpace(raw.AuthorizationURL), UserCode: strings.TrimSpace(raw.UserCode), FailureCode: strings.TrimSpace(raw.FailureCode), ExpiresAt: raw.ExpiresAt}
	if len(raw.TargetRef) > 128 {
		return OAuthEvent{}, ErrInvalidResponse
	}
	event.TargetRef = raw.TargetRef
	if !validProvider(event.Provider) || event.State == "" || len(event.State) > 256 || event.ExpiresAt.IsZero() {
		return OAuthEvent{}, ErrInvalidResponse
	}
	if event.Mode == "" {
		event.Mode = OAuthModeCallback
	}
	if event.Mode != OAuthModeCallback && (event.Provider != ProviderCodex || event.Mode != OAuthModeDevice) {
		return OAuthEvent{}, ErrInvalidResponse
	}
	if event.AuthorizationURL != "" {
		parsed, err := url.Parse(event.AuthorizationURL)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
			return OAuthEvent{}, ErrInvalidResponse
		}
	}
	if len(event.UserCode) > 128 || (event.Mode == OAuthModeDevice && event.Status == OAuthPending && (event.AuthorizationURL == "" || event.UserCode == "")) {
		return OAuthEvent{}, ErrInvalidResponse
	}
	switch event.Status {
	case OAuthPending, OAuthCompleted, OAuthFailed, OAuthExpired:
	default:
		return OAuthEvent{}, ErrInvalidResponse
	}
	if event.Status == OAuthFailed {
		event.FailureCode = boundedOAuthFailure(event.FailureCode)
	} else if event.Status == OAuthExpired && event.FailureCode != "cancelled" {
		event.FailureCode = "expired"
	} else if event.Status != OAuthExpired {
		event.FailureCode = ""
	}
	return event, nil
}

func boundedOAuthFailure(code string) string {
	switch code {
	case "identity_mismatch", "credential_changed", "storage_unavailable":
		return code
	default:
		return "authentication_failed"
	}
}

// CredentialPublicID hides the runner reference behind an installation-scoped identifier.
func (c *ManagementClient) CredentialPublicID(ref string) (string, error) {
	return c.publicID("account", ref, "amc_")
}

// OAuthPublicID hides callback state behind a separately namespaced identifier.
func (c *ManagementClient) OAuthPublicID(state string) (string, error) {
	return c.publicID("oauth", state, "amo_")
}

func (c *ManagementClient) publicID(namespace, value, prefix string) (string, error) {
	if c == nil || c.source == nil || strings.TrimSpace(value) == "" {
		return "", ErrUnavailable
	}
	endpoint, ready := c.source.Endpoint()
	if !ready || strings.TrimSpace(endpoint.ManagementToken) == "" {
		return "", ErrUnavailable
	}
	mac := hmac.New(sha256.New, []byte(endpoint.ManagementToken))
	_, _ = mac.Write([]byte(namespace + "\x00" + value))
	return prefix + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:18]), nil
}
