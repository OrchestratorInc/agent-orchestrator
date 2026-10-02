package runner

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	sdkauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

func (c *oauthCoordinator) handleManagedStart(w http.ResponseWriter, request *http.Request) {
	var input struct {
		Provider   string `json:"provider"`
		Mode       string `json:"mode"`
		TargetRef  string `json:"targetRef,omitempty"`
		Generation uint64 `json:"generation,omitempty"`
	}
	if decodeCredentialJSON(request, &input) != nil || !validVaultProvider(input.Provider) || len(input.TargetRef) > 128 || (input.TargetRef == "") != (input.Generation == 0) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if input.Mode == "" {
		input.Mode = "callback"
	}
	if input.Mode != "callback" && (input.Provider != "codex" || input.Mode != "device") {
		writeOAuthError(w, http.StatusBadRequest, "unsupported_mode")
		return
	}
	if input.Mode == "device" && c.startCodexDevice == nil {
		writeOAuthError(w, http.StatusServiceUnavailable, "device_login_unavailable")
		return
	}
	targetID := ""
	if input.TargetRef != "" {
		h := credentialHTTP{runtime: c.credentials}
		auth, err := h.byRef(request.Context(), input.TargetRef)
		if err != nil {
			writeCredentialError(w, err)
			return
		}
		if auth == nil || auth.Provider != input.Provider {
			writeOAuthError(w, http.StatusNotFound, "account_not_found")
			return
		}
		targetID = auth.ID
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		writeOAuthError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	if current := c.sessions[c.providers[input.Provider]]; current != nil && current.status == "pending" {
		if current.authorizationURL == "" || current.mode != input.Mode || current.targetRef != input.TargetRef || current.targetGeneration != input.Generation {
			c.mu.Unlock()
			writeOAuthError(w, http.StatusConflict, "oauth_busy")
		} else {
			snapshot := *current
			c.mu.Unlock()
			writeOAuthSession(w, &snapshot)
		}
		return
	}
	var listener net.Listener
	if input.Mode == "callback" {
		var err error
		listener, err = c.listen("tcp4", oauthProviders[input.Provider].callbackAddr)
		if err != nil {
			c.mu.Unlock()
			writeOAuthError(w, http.StatusConflict, "callback_unavailable")
			return
		}
	}
	id, err := randomVaultID()
	if err != nil {
		if listener != nil {
			_ = listener.Close()
		}
		c.mu.Unlock()
		writeOAuthError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	duration := oauthSessionDuration
	if input.Mode == "device" {
		duration = codexDeviceDuration
	}
	ctx, cancel := context.WithTimeout(c.runContext, duration)
	expiresAt, _ := ctx.Deadline()
	if err := c.credentials.vault.beginProviderLogin(request.Context(), id, input.Provider, expiresAt, targetID, input.Generation); err != nil {
		cancel()
		if listener != nil {
			_ = listener.Close()
		}
		c.mu.Unlock()
		if errors.Is(err, errCredentialIdentity) {
			writeOAuthError(w, http.StatusUnprocessableEntity, "identity_unavailable")
		} else {
			writeCredentialError(w, err)
		}
		return
	}
	session := &oauthRunnerSession{provider: input.Provider, mode: input.Mode, targetRef: input.TargetRef, targetGeneration: input.Generation, state: id, status: "pending", expiresAt: expiresAt, listener: listener, cancel: cancel}
	c.sessions[id], c.providers[input.Provider] = session, id
	ready, done := make(chan struct{}, 1), make(chan struct{})
	c.workers.Add(1)
	c.mu.Unlock()
	go func() {
		defer c.workers.Done()
		defer close(done)
		deliver := func(ctx context.Context, raw, code string) error {
			authorizationURL, valid := validAuthorizationURL(raw)
			if !valid {
				return errCredentialConflict
			}
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.closed || session.status != "pending" || ctx.Err() != nil {
				return errCredentialFenced
			}
			session.authorizationURL = authorizationURL
			session.userCode = code
			c.publishLocked(session)
			select {
			case ready <- struct{}{}:
			default:
			}
			return nil
		}
		var err error
		if input.Mode == "device" {
			_, err = c.credentials.ConnectDevice(ctx, id, c.startCodexDevice, func(login codexDeviceLogin) error {
				return deliver(ctx, login.AuthorizationURL, login.UserCode)
			})
		} else {
			_, err = c.credentials.ConnectBrowser(ctx, id, c.authenticator(input.Provider), c.config, listener, func(ctx context.Context, raw string) error {
				return deliver(ctx, raw, "")
			})
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		status, failure := "completed", ""
		if err != nil {
			status, failure = "failed", "authentication_failed"
			switch {
			case errors.Is(err, context.DeadlineExceeded):
				status, failure = "expired", "expired"
			case errors.Is(err, errCredentialIdentity):
				failure = "identity_mismatch"
			case errors.Is(err, errCredentialConflict), errors.Is(err, errCredentialFenced):
				failure = "credential_changed"
			case errors.Is(err, errCredentialStorage):
				failure = "storage_unavailable"
			}
		}
		c.finishSessionLocked(session, status, failure)
	}()
	select {
	case <-ready:
	case <-done:
	case <-request.Context().Done():
		cleanup, stop := context.WithTimeout(context.WithoutCancel(request.Context()), 5*time.Second)
		defer stop()
		if err := c.credentials.vault.Cancel(cleanup, id); err != nil && !errors.Is(err, errCredentialCommitted) {
			c.mu.Lock()
			c.finishSessionLocked(session, "failed", "storage_unavailable")
			c.mu.Unlock()
		}
		cancel()
		return
	case <-ctx.Done():
	}
	c.mu.Lock()
	snapshot := *session
	c.mu.Unlock()
	if snapshot.authorizationURL != "" {
		writeOAuthSession(w, &snapshot)
	} else if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		writeOAuthError(w, http.StatusGatewayTimeout, "oauth_start_failed")
	} else {
		writeOAuthError(w, http.StatusBadGateway, "oauth_start_failed")
	}
}

func (c *oauthCoordinator) managedStatus(w http.ResponseWriter, request *http.Request) {
	state := strings.TrimSpace(request.URL.Query().Get("state"))
	if !validVaultID(state) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_state")
		return
	}
	c.mu.Lock()
	status, failure := "expired", "expired"
	if session := c.sessions[state]; session != nil {
		status, failure = session.status, session.failureCode
	}
	c.mu.Unlock()
	writeCredentialJSON(w, map[string]string{"status": status, "failureCode": failure})
}

func (c *oauthCoordinator) managedCancel(w http.ResponseWriter, request *http.Request) {
	state := strings.TrimSpace(request.URL.Query().Get("state"))
	if !validVaultID(state) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_state")
		return
	}
	if err := c.credentials.vault.Cancel(request.Context(), state); err != nil {
		if errors.Is(err, errCredentialCommitted) {
			writeOAuthError(w, http.StatusConflict, "already_completed")
		} else {
			writeCredentialError(w, err)
		}
		return
	}
	c.mu.Lock()
	if session := c.sessions[state]; session != nil {
		c.finishSessionLocked(session, "expired", "cancelled")
	}
	c.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func managedAuthenticator(provider string) sdkauth.Authenticator {
	if provider == "codex" {
		return sdkauth.NewCodexAuthenticator()
	}
	return sdkauth.NewClaudeAuthenticator()
}

func (c *oauthCoordinator) useCredentials(ctx context.Context, runtime *credentialRuntime, cfg *sdkconfig.Config) {
	c.credentials, c.config, c.runContext, c.authenticator = runtime, cfg, ctx, managedAuthenticator
}
