package accountsmanager

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OAuthState describes login progress, independently of account health.
type OAuthState string

// OAuthMode selects browser callback or supported device-code authentication.
type OAuthMode string

// OAuthPending and the other constants define supported login states and modes.
const (
	OAuthPending      OAuthState = "pending"
	OAuthCompleted    OAuthState = "completed"
	OAuthFailed       OAuthState = "failed"
	OAuthExpired      OAuthState = "expired"
	OAuthModeCallback OAuthMode  = "callback"
	OAuthModeDevice   OAuthMode  = "device"
)

// OAuthSession contains private callback state and user-facing sign-in instructions.
type OAuthSession struct {
	TargetRef        string
	Provider         Provider
	Mode             OAuthMode
	State            string
	AuthorizationURL string
	UserCode         string
	ExpiresAt        time.Time
}

// OAuthStatus carries a bounded failure code rather than a provider error body.
type OAuthStatus struct {
	State       OAuthState
	FailureCode string
}

// StartOAuth validates that returned instructions match the requested provider and mode.
func (c *ManagementClient) StartOAuth(ctx context.Context, provider Provider, mode OAuthMode) (OAuthSession, error) {
	return c.startOAuth(ctx, provider, mode, "", 0)
}

// ReconnectOAuth replaces only the explicitly selected credential generation.
func (c *ManagementClient) ReconnectOAuth(ctx context.Context, provider Provider, mode OAuthMode, ref string, generation uint64) (OAuthSession, error) {
	if strings.TrimSpace(ref) == "" || len(ref) > 128 || generation == 0 {
		return OAuthSession{}, ErrInvalidCredential
	}
	return c.startOAuth(ctx, provider, mode, ref, generation)
}

func (c *ManagementClient) startOAuth(ctx context.Context, provider Provider, mode OAuthMode, ref string, generation uint64) (OAuthSession, error) {
	if !validProvider(provider) {
		return OAuthSession{}, ErrUnsupportedProvider
	}
	if mode == "" {
		mode = OAuthModeCallback
	}
	if mode != OAuthModeCallback && (provider != ProviderCodex || mode != OAuthModeDevice) {
		return OAuthSession{}, ErrOperationUnsupported
	}
	var response struct {
		TargetRef        string    `json:"targetRef"`
		Provider         string    `json:"provider"`
		Mode             string    `json:"mode"`
		State            string    `json:"state"`
		AuthorizationURL string    `json:"authorizationUrl"`
		UserCode         string    `json:"userCode"`
		ExpiresAt        time.Time `json:"expiresAt"`
	}
	err := c.doJSON(ctx, "start OAuth", http.MethodPost, "/ao/internal/oauth/start", struct {
		Provider   Provider  `json:"provider"`
		Mode       OAuthMode `json:"mode"`
		TargetRef  string    `json:"targetRef,omitempty"`
		Generation uint64    `json:"generation,omitempty"`
	}{provider, mode, ref, generation}, &response)
	if err != nil {
		var statusErr *ManagementStatusError
		if errors.As(err, &statusErr) {
			switch statusErr.StatusCode {
			case http.StatusBadRequest:
				return OAuthSession{}, ErrUnsupportedProvider
			case http.StatusConflict:
				return OAuthSession{}, ErrOAuthBusy
			case http.StatusNotFound:
				return OAuthSession{}, ErrCredentialNotFound
			case http.StatusUnprocessableEntity:
				return OAuthSession{}, ErrOperationUnsupported
			}
		}
		return OAuthSession{}, err
	}
	parsedURL, parseErr := url.Parse(strings.TrimSpace(response.AuthorizationURL))
	state := strings.TrimSpace(response.State)
	responseProvider := Provider(strings.ToLower(strings.TrimSpace(response.Provider)))
	responseMode := OAuthMode(strings.ToLower(strings.TrimSpace(response.Mode)))
	userCode := strings.TrimSpace(response.UserCode)
	if parseErr != nil || parsedURL.Scheme != "https" || parsedURL.Host == "" || parsedURL.User != nil || state == "" || response.TargetRef != ref || responseProvider != provider || responseMode != mode || response.ExpiresAt.IsZero() || (mode == OAuthModeDevice && userCode == "") || len(userCode) > 128 {
		return OAuthSession{}, ErrInvalidResponse
	}
	return OAuthSession{
		TargetRef:        ref,
		Provider:         responseProvider,
		Mode:             responseMode,
		State:            state,
		AuthorizationURL: parsedURL.String(),
		UserCode:         userCode,
		ExpiresAt:        response.ExpiresAt,
	}, nil
}

// GetOAuthStatus maps runner progress to bounded states and sanitized failures.
func (c *ManagementClient) GetOAuthStatus(ctx context.Context, state string) (OAuthStatus, error) {
	state = strings.TrimSpace(state)
	if state == "" || len(state) > 256 {
		return OAuthStatus{}, ErrOAuthExpired
	}
	query := url.Values{"state": []string{state}}
	var response struct {
		Status      string `json:"status"`
		FailureCode string `json:"failureCode"`
	}
	if err := c.doJSON(ctx, "read OAuth status", http.MethodGet, "/ao/internal/oauth/status?"+query.Encode(), nil, &response); err != nil {
		return OAuthStatus{}, err
	}
	switch OAuthState(strings.TrimSpace(response.Status)) {
	case OAuthPending:
		return OAuthStatus{State: OAuthPending}, nil
	case OAuthCompleted:
		return OAuthStatus{State: OAuthCompleted}, nil
	case OAuthFailed:
		return OAuthStatus{State: OAuthFailed, FailureCode: boundedOAuthFailure(response.FailureCode)}, nil
	case OAuthExpired:
		return OAuthStatus{State: OAuthExpired}, ErrOAuthExpired
	default:
		return OAuthStatus{}, ErrInvalidResponse
	}
}

// CancelOAuth accepts an empty state as an already-cancelled operation.
func (c *ManagementClient) CancelOAuth(ctx context.Context, state string) error {
	state = strings.TrimSpace(state)
	if state == "" {
		return nil
	}
	if len(state) > 256 {
		return ErrOAuthExpired
	}
	query := url.Values{"state": []string{state}}
	return c.doJSON(ctx, "cancel OAuth", http.MethodDelete, "/ao/internal/oauth/session?"+query.Encode(), nil, nil)
}

func validProvider(provider Provider) bool {
	return provider == ProviderCodex || provider == ProviderClaude
}
