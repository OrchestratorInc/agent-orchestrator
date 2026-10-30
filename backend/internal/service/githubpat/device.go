package githubpat

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// defaultOAuthClientID is the public client ID of the Agent Orchestrator
// GitHub OAuth App. Device flow needs no client secret, so shipping the ID in
// the binary is safe. AO_GITHUB_OAUTH_CLIENT_ID overrides it.
const defaultOAuthClientID = "Ov23lieqORSXeDaVaJ7D"

// deviceLoginScopes match what agents need: repo for clone/push/PRs/issues,
// read:org for org repos, workflow so pushes may touch .github/workflows.
const deviceLoginScopes = "repo read:org workflow"

// ErrDeviceLoginUnavailable reports that this build has no OAuth client ID.
var ErrDeviceLoginUnavailable = errors.New("GitHub sign-in is not configured in this build")

// DeviceLoginState is the progress of a GitHub device-flow sign-in.
type DeviceLoginState string

const (
	DeviceLoginIdle      DeviceLoginState = "idle"
	DeviceLoginPending   DeviceLoginState = "pending"
	DeviceLoginSucceeded DeviceLoginState = "succeeded"
	DeviceLoginFailed    DeviceLoginState = "failed"
)

// DeviceLogin is the current device-flow sign-in. UserCode and VerificationURI
// are what the user enters on GitHub while the state is pending.
type DeviceLogin struct {
	State           DeviceLoginState
	UserCode        string
	VerificationURI string
	ExpiresAt       time.Time
	Login           string
	Error           string

	deviceCode string
}

// StartDeviceLogin begins a GitHub device-flow sign-in, or returns the one
// already pending. The daemon polls GitHub in the background and stores the
// token once the user approves.
func (s *Service) StartDeviceLogin(ctx context.Context) (DeviceLogin, error) {
	if s.clientID == "" {
		return DeviceLogin{}, ErrDeviceLoginUnavailable
	}
	s.deviceMu.Lock()
	defer s.deviceMu.Unlock()
	if s.device.State == DeviceLoginPending && s.clock().Before(s.device.ExpiresAt) {
		return s.device, nil
	}
	code, err := s.requestDeviceCode(ctx)
	if err != nil {
		return DeviceLogin{}, err
	}
	if s.deviceCancel != nil {
		s.deviceCancel()
	}
	pollCtx, cancel := context.WithDeadline(context.Background(), code.ExpiresAt)
	s.deviceCancel = cancel
	s.device = code.DeviceLogin
	go s.pollDeviceLogin(pollCtx, code.deviceCode, code.interval)
	return s.device, nil
}

// DeviceLoginStatus returns the current sign-in, or an idle one.
func (s *Service) DeviceLoginStatus() DeviceLogin {
	s.deviceMu.Lock()
	defer s.deviceMu.Unlock()
	if s.device.State == "" {
		return DeviceLogin{State: DeviceLoginIdle}
	}
	return s.device
}

// CancelDeviceLogin stops a pending sign-in and resets the state to idle.
func (s *Service) CancelDeviceLogin() {
	s.deviceMu.Lock()
	defer s.deviceMu.Unlock()
	if s.deviceCancel != nil {
		s.deviceCancel()
		s.deviceCancel = nil
	}
	s.device = DeviceLogin{State: DeviceLoginIdle}
}

type pendingDeviceCode struct {
	DeviceLogin
	interval time.Duration
}

func (s *Service) requestDeviceCode(ctx context.Context) (pendingDeviceCode, error) {
	var out struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
		Error           string `json:"error"`
		ErrorDesc       string `json:"error_description"`
	}
	form := url.Values{"client_id": {s.clientID}, "scope": {deviceLoginScopes}}
	if err := s.postOAuthForm(ctx, "/login/device/code", form, &out); err != nil {
		return pendingDeviceCode{}, fmt.Errorf("github device code: %w", err)
	}
	if out.Error != "" {
		return pendingDeviceCode{}, fmt.Errorf("github device code: %s", firstNonEmpty(out.ErrorDesc, out.Error))
	}
	if out.DeviceCode == "" || out.UserCode == "" {
		return pendingDeviceCode{}, errors.New("github device code: incomplete response")
	}
	return pendingDeviceCode{
		DeviceLogin: DeviceLogin{
			State:           DeviceLoginPending,
			UserCode:        out.UserCode,
			VerificationURI: firstNonEmpty(out.VerificationURI, "https://github.com/login/device"),
			ExpiresAt:       s.clock().Add(time.Duration(out.ExpiresIn) * time.Second),
			deviceCode:      out.DeviceCode,
		},
		interval: time.Duration(cmp.Or(out.Interval, 5)) * time.Second,
	}, nil
}

// pollDeviceLogin asks GitHub for the token at the interval GitHub requires
// until the user approves, denies, or the code expires. Network errors are
// retried, since the user may still be approving in the browser.
func (s *Service) pollDeviceLogin(ctx context.Context, deviceCode string, interval time.Duration) {
	for {
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				s.finishDeviceLogin(deviceCode, DeviceLoginFailed, "", "The sign-in code expired. Start again.")
			}
			return
		case <-time.After(interval):
		}
		var out struct {
			AccessToken           string `json:"access_token"`
			RefreshToken          string `json:"refresh_token"`
			ExpiresIn             int    `json:"expires_in"`
			RefreshTokenExpiresIn int    `json:"refresh_token_expires_in"`
			Error                 string `json:"error"`
			ErrorDesc             string `json:"error_description"`
		}
		form := url.Values{
			"client_id":   {s.clientID},
			"device_code": {deviceCode},
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		}
		if err := s.postOAuthForm(ctx, "/login/oauth/access_token", form, &out); err != nil {
			continue
		}
		switch out.Error {
		case "":
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 5 * time.Second
			continue
		case "access_denied":
			s.finishDeviceLogin(deviceCode, DeviceLoginFailed, "", "GitHub sign-in was cancelled.")
			return
		case "expired_token":
			s.finishDeviceLogin(deviceCode, DeviceLoginFailed, "", "The sign-in code expired. Start again.")
			return
		default:
			s.finishDeviceLogin(deviceCode, DeviceLoginFailed, "", firstNonEmpty(out.ErrorDesc, out.Error))
			return
		}
		if out.AccessToken == "" {
			s.finishDeviceLogin(deviceCode, DeviceLoginFailed, "", "GitHub did not return an access token.")
			return
		}
		var err error
		if out.RefreshToken != "" {
			now := s.clock()
			err = s.StoreOAuthToken(ctx, out.AccessToken, out.RefreshToken,
				expiryFrom(now, out.ExpiresIn, time.Time{}), expiryFrom(now, out.RefreshTokenExpiresIn, time.Time{}))
		} else {
			err = s.StorePAT(ctx, out.AccessToken)
		}
		if err != nil {
			s.finishDeviceLogin(deviceCode, DeviceLoginFailed, "", "Could not save the GitHub token: "+err.Error())
			return
		}
		s.finishDeviceLogin(deviceCode, DeviceLoginSucceeded, s.fetchLogin(ctx, out.AccessToken), "")
		return
	}
}

// finishDeviceLogin records the outcome unless a newer sign-in replaced it.
func (s *Service) finishDeviceLogin(deviceCode string, state DeviceLoginState, login, message string) {
	s.deviceMu.Lock()
	defer s.deviceMu.Unlock()
	if s.device.deviceCode != deviceCode {
		return
	}
	s.device.State = state
	s.device.Login = login
	s.device.Error = message
	s.device.UserCode = ""
	if s.deviceCancel != nil {
		s.deviceCancel()
		s.deviceCancel = nil
	}
}

func (s *Service) postOAuthForm(ctx context.Context, path string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.oauthURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// fetchLogin returns the GitHub username for token, or "" when it cannot be read.
func (s *Service) fetchLogin(ctx context.Context, token string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.apiURL+"/user", http.NoBody)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	var user struct {
		Login string `json:"login"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&user) != nil {
		return ""
	}
	return user.Login
}
