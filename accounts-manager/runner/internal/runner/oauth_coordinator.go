package runner

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	sdkauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/auth"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

const (
	oauthSessionDuration   = 5 * time.Minute
	codexDeviceDuration    = 15 * time.Minute
	oauthTerminalRetention = time.Minute
	oauthQueryLimit        = 16 << 10
	oauthResponseLimit     = 1 << 20
)

type oauthProviderConfig struct {
	upstreamPath string
	callbackAddr string
	callbackPath string
}

var oauthProviders = map[string]oauthProviderConfig{
	"codex": {
		upstreamPath: "/v0/management/codex-auth-url",
		callbackAddr: "127.0.0.1:1455",
		callbackPath: "/auth/callback",
	},
	"claude": {
		upstreamPath: "/v0/management/anthropic-auth-url",
		callbackAddr: "127.0.0.1:54545",
		callbackPath: "/callback",
	},
}

type oauthRunnerSession struct {
	targetRef        string
	targetGeneration uint64
	provider         string
	mode             string
	state            string
	authorizationURL string
	userCode         string
	expiresAt        time.Time
	status           string
	failureCode      string
	listener         net.Listener
	cancel           context.CancelFunc
}

type oauthSessionEvent struct {
	TargetRef        string    `json:"targetRef,omitempty"`
	Provider         string    `json:"provider"`
	Mode             string    `json:"mode"`
	State            string    `json:"state"`
	Status           string    `json:"status"`
	AuthorizationURL string    `json:"authorizationUrl,omitempty"`
	UserCode         string    `json:"userCode,omitempty"`
	FailureCode      string    `json:"failureCode,omitempty"`
	ExpiresAt        time.Time `json:"expiresAt"`
}

type oauthCoordinator struct {
	credentials      *credentialRuntime
	config           *sdkconfig.Config
	runContext       context.Context
	authenticator    func(string) sdkauth.Authenticator
	workers          sync.WaitGroup
	baseURL          string
	managementKey    string
	client           *http.Client
	listen           func(string, string) (net.Listener, error)
	startCodexDevice func(context.Context) (codexDeviceLogin, error)

	mu          sync.Mutex
	sessions    map[string]*oauthRunnerSession
	providers   map[string]string
	subscribers map[chan oauthSessionEvent]struct{}
	closed      bool

	observeInterval        time.Duration
	eventHeartbeatInterval time.Duration
}

type codexDeviceLogin struct {
	AuthorizationURL string
	UserCode         string
	Done             <-chan error
	Result           <-chan *coreauth.Auth
	close            func()
}

func newOAuthCoordinator(baseURL, managementKey string, client *http.Client, listen func(string, string) (net.Listener, error)) *oauthCoordinator {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	if listen == nil {
		listen = net.Listen
	}
	return &oauthCoordinator{
		baseURL:                strings.TrimRight(baseURL, "/"),
		managementKey:          managementKey,
		client:                 client,
		listen:                 listen,
		sessions:               make(map[string]*oauthRunnerSession),
		providers:              make(map[string]string),
		subscribers:            make(map[chan oauthSessionEvent]struct{}),
		observeInterval:        250 * time.Millisecond,
		eventHeartbeatInterval: 15 * time.Second,
	}
}

func (c *oauthCoordinator) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !validControlAuthorization(r.Header.Get("Authorization"), c.managementKey) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/ao/internal/oauth/start":
		if c.credentials != nil {
			c.handleManagedStart(w, r)
			return
		}
		c.handleStart(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/ao/internal/oauth/status":
		if c.credentials != nil {
			c.managedStatus(w, r)
			return
		}
		c.handleStatus(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/ao/internal/oauth/events":
		c.handleEvents(w, r)
	case r.Method == http.MethodDelete && r.URL.Path == "/ao/internal/oauth/session":
		if c.credentials != nil {
			c.managedCancel(w, r)
			return
		}
		c.handleCancel(w, r)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (c *oauthCoordinator) handleStart(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Provider string `json:"provider"`
		Mode     string `json:"mode"`
	}
	if err := decodeBoundedJSON(r.Body, &input); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	provider := strings.ToLower(strings.TrimSpace(input.Provider))
	mode := strings.ToLower(strings.TrimSpace(input.Mode))
	if mode == "" {
		mode = "callback"
	}
	if mode != "callback" && (provider != "codex" || mode != "device") {
		writeOAuthError(w, http.StatusBadRequest, "unsupported_mode")
		return
	}
	config, ok := oauthProviders[provider]
	if !ok {
		writeOAuthError(w, http.StatusBadRequest, "unsupported_provider")
		return
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		writeOAuthError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	if state := c.providers[provider]; state != "" {
		if state == "starting" {
			c.mu.Unlock()
			writeOAuthError(w, http.StatusConflict, "oauth_busy")
			return
		}
		if existing := c.sessions[state]; existing != nil && existing.status == "pending" && time.Now().Before(existing.expiresAt) {
			c.mu.Unlock()
			writeOAuthSession(w, existing)
			return
		}
		delete(c.providers, provider)
	}
	if mode == "device" {
		c.providers[provider] = "starting"
		c.mu.Unlock()
		c.startDevice(w)
		return
	}
	defer c.mu.Unlock()

	listener, err := c.listen("tcp", config.callbackAddr)
	if err != nil {
		writeOAuthError(w, http.StatusConflict, "callback_unavailable")
		return
	}

	var upstream struct {
		Status string `json:"status"`
		URL    string `json:"url"`
		State  string `json:"state"`
	}
	if err = c.upstreamJSON(r.Context(), http.MethodGet, config.upstreamPath, nil, &upstream); err != nil {
		_ = listener.Close()
		writeOAuthError(w, http.StatusBadGateway, "oauth_start_failed")
		return
	}
	authorizationURL, valid := validAuthorizationURL(upstream.URL)
	state := strings.TrimSpace(upstream.State)
	if upstream.Status != "ok" || !valid || state == "" || len(state) > 256 {
		_ = listener.Close()
		writeOAuthError(w, http.StatusBadGateway, "invalid_upstream_response")
		return
	}
	if _, exists := c.sessions[state]; exists {
		_ = listener.Close()
		writeOAuthError(w, http.StatusBadGateway, "invalid_upstream_response")
		return
	}

	session := &oauthRunnerSession{
		provider:         provider,
		mode:             mode,
		state:            state,
		authorizationURL: authorizationURL,
		expiresAt:        time.Now().Add(oauthSessionDuration),
		status:           "pending",
		listener:         listener,
	}
	c.sessions[state] = session
	c.providers[provider] = state
	c.publishLocked(session)

	server := &http.Server{
		Handler:           c.callbackHandler(session),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      5 * time.Second,
	}
	go func() {
		_ = server.Serve(listener)
	}()
	go c.expireSession(state, session.expiresAt)
	go c.observeSession(state, session.expiresAt) // #nosec G118 -- Observation spans requests and stops on operation expiry or coordinator closure.
	writeOAuthSession(w, session)
}

func (c *oauthCoordinator) startDevice(w http.ResponseWriter) {
	clearStarting := func() {
		c.mu.Lock()
		if c.providers["codex"] == "starting" {
			delete(c.providers, "codex")
		}
		c.mu.Unlock()
	}
	if c.startCodexDevice == nil {
		clearStarting()
		writeOAuthError(w, http.StatusServiceUnavailable, "device_login_unavailable")
		return
	}
	stateBytes := make([]byte, 24)
	if _, err := rand.Read(stateBytes); err != nil {
		clearStarting()
		writeOAuthError(w, http.StatusServiceUnavailable, "oauth_start_failed")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), codexDeviceDuration)
	login, err := c.startCodexDevice(ctx)
	if err != nil {
		cancel()
		clearStarting()
		writeOAuthError(w, http.StatusBadGateway, "oauth_start_failed")
		return
	}
	authorizationURL, valid := validAuthorizationURL(login.AuthorizationURL)
	userCode := strings.TrimSpace(login.UserCode)
	if !valid || userCode == "" || len(userCode) > 128 || login.Done == nil {
		cancel()
		clearStarting()
		writeOAuthError(w, http.StatusBadGateway, "invalid_upstream_response")
		return
	}
	state := hex.EncodeToString(stateBytes)
	session := &oauthRunnerSession{
		provider: "codex", mode: "device", state: state,
		authorizationURL: authorizationURL, userCode: userCode,
		expiresAt: time.Now().Add(codexDeviceDuration), status: "pending", cancel: cancel,
	}
	c.mu.Lock()
	if c.closed || c.providers["codex"] != "starting" {
		c.mu.Unlock()
		cancel()
		writeOAuthError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	c.sessions[state] = session
	c.providers[session.provider] = state
	c.publishLocked(session)
	c.mu.Unlock()
	go c.awaitDeviceLogin(state, session, login.Done)
	go c.expireSession(state, session.expiresAt)
	writeOAuthSession(w, session)
}

func (c *oauthCoordinator) awaitDeviceLogin(state string, session *oauthRunnerSession, done <-chan error) {
	err, open := <-done
	c.mu.Lock()
	defer c.mu.Unlock()
	if !open || c.sessions[state] != session || session.status != "pending" {
		return
	}
	if err != nil {
		c.finishSessionLocked(session, "failed", "authentication_failed")
		return
	}
	c.finishSessionLocked(session, "completed", "")
}

func (c *oauthCoordinator) handleStatus(w http.ResponseWriter, r *http.Request) {
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if state == "" || len(state) > 256 {
		writeOAuthError(w, http.StatusBadRequest, "invalid_state")
		return
	}

	c.mu.Lock()
	session := c.sessions[state]
	if session == nil {
		c.mu.Unlock()
		writeOAuthStatus(w, "expired")
		return
	}
	if !time.Now().Before(session.expiresAt) || session.status == "expired" {
		c.expireSessionLocked(session)
		c.mu.Unlock()
		writeOAuthStatus(w, "expired")
		return
	}
	if session.status != "pending" {
		status := session.status
		c.mu.Unlock()
		writeOAuthStatus(w, status)
		return
	}
	if session.mode == "device" {
		c.mu.Unlock()
		writeOAuthStatus(w, "pending")
		return
	}
	c.mu.Unlock()

	query := url.Values{"state": []string{state}}
	var upstream struct {
		Status string `json:"status"`
	}
	if err := c.upstreamJSON(r.Context(), http.MethodGet, "/v0/management/get-auth-status?"+query.Encode(), nil, &upstream); err != nil {
		writeOAuthError(w, http.StatusBadGateway, "oauth_status_failed")
		return
	}
	status := "failed"
	switch upstream.Status {
	case "wait":
		status = "pending"
	case "ok":
		status = "completed"
	case "error":
		status = "failed"
	}
	if status != "pending" {
		c.mu.Lock()
		if current := c.sessions[state]; current == session {
			c.finishSessionLocked(current, status, failureCodeForOAuthStatus(status))
		}
		c.mu.Unlock()
	}
	writeOAuthStatus(w, status)
}

func (c *oauthCoordinator) handleCancel(w http.ResponseWriter, r *http.Request) {
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if state == "" || len(state) > 256 {
		writeOAuthError(w, http.StatusBadRequest, "invalid_state")
		return
	}
	c.mu.Lock()
	session := c.sessions[state]
	if session == nil || session.status != "pending" {
		c.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	c.mu.Unlock()
	if session.mode == "device" {
		c.mu.Lock()
		if current := c.sessions[state]; current == session && current.status == "pending" {
			c.finishSessionLocked(current, "expired", "cancelled")
		}
		c.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	}

	query := url.Values{"state": []string{state}}
	var ignored map[string]any
	if err := c.upstreamJSON(r.Context(), http.MethodDelete, "/v0/management/oauth-session?"+query.Encode(), nil, &ignored); err != nil {
		writeOAuthError(w, http.StatusBadGateway, "oauth_cancel_failed")
		return
	}
	c.mu.Lock()
	if current := c.sessions[state]; current == session && current.status == "pending" {
		c.finishSessionLocked(current, "expired", "cancelled")
	}
	c.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (c *oauthCoordinator) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeOAuthError(w, http.StatusInternalServerError, "streaming_unsupported")
		return
	}
	updates := make(chan oauthSessionEvent, 16)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		writeOAuthError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	states := make([]string, 0, len(c.sessions))
	for state := range c.sessions {
		states = append(states, state)
	}
	sort.Strings(states)
	replay := make([]oauthSessionEvent, 0, len(states))
	for _, state := range states {
		replay = append(replay, eventFromSession(c.sessions[state]))
	}
	c.subscribers[updates] = struct{}{}
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.subscribers, updates)
		c.mu.Unlock()
	}()

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	for _, event := range replay {
		if !writeOAuthEvent(w, flusher, event) {
			return
		}
	}
	interval := c.eventHeartbeatInterval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	heartbeat := time.NewTicker(interval)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event, open := <-updates:
			if !open || !writeOAuthEvent(w, flusher, event) {
				return
			}
		case <-heartbeat.C:
			if _, err := io.WriteString(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeOAuthEvent(w http.ResponseWriter, flusher http.Flusher, event oauthSessionEvent) bool {
	data, err := json.Marshal(event)
	if err != nil {
		return false
	}
	if _, err = io.WriteString(w, "event: oauth_session\ndata: "+string(data)+"\n\n"); err != nil {
		return false
	}
	flusher.Flush()
	return true
}

func eventFromSession(session *oauthRunnerSession) oauthSessionEvent {
	if session == nil {
		return oauthSessionEvent{}
	}
	event := oauthSessionEvent{
		TargetRef: session.targetRef,
		Provider:  session.provider, Mode: session.mode, State: session.state, Status: session.status,
		FailureCode: session.failureCode, ExpiresAt: session.expiresAt,
	}
	if session.mode == "device" {
		event.AuthorizationURL = session.authorizationURL
		event.UserCode = session.userCode
	}
	return event
}

func (c *oauthCoordinator) publishLocked(session *oauthRunnerSession) {
	event := eventFromSession(session)
	for subscriber := range c.subscribers {
		select {
		case subscriber <- event:
		default:
		}
	}
}

func (c *oauthCoordinator) observeSession(state string, expiresAt time.Time) {
	interval := c.observeInterval
	if interval <= 0 {
		interval = 250 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		c.mu.Lock()
		session := c.sessions[state]
		pending := !c.closed && session != nil && session.status == "pending" && session.expiresAt.Equal(expiresAt)
		c.mu.Unlock()
		if !pending {
			return
		}
		query := url.Values{"state": []string{state}}
		var upstream struct {
			Status string `json:"status"`
		}
		if err := c.upstreamJSON(context.Background(), http.MethodGet, "/v0/management/get-auth-status?"+query.Encode(), nil, &upstream); err != nil {
			continue
		}
		status := "pending"
		switch upstream.Status {
		case "ok":
			status = "completed"
		case "error":
			status = "failed"
		}
		if status == "pending" {
			continue
		}
		c.mu.Lock()
		if current := c.sessions[state]; current != nil && current.status == "pending" && current.expiresAt.Equal(expiresAt) {
			c.finishSessionLocked(current, status, failureCodeForOAuthStatus(status))
		}
		c.mu.Unlock()
		return
	}
}

func failureCodeForOAuthStatus(status string) string {
	if status == "failed" {
		return "authentication_failed"
	}
	return ""
}

func (c *oauthCoordinator) finishSessionLocked(session *oauthRunnerSession, status, failureCode string) {
	if session == nil || session.status != "pending" {
		return
	}
	session.status = status
	session.failureCode = failureCode
	if session.cancel != nil {
		session.cancel()
		session.cancel = nil
	}
	c.closeListenerLocked(session)
	delete(c.providers, session.provider)
	c.publishLocked(session)
	state := session.state
	go func() {
		timer := time.NewTimer(oauthTerminalRetention)
		defer timer.Stop()
		<-timer.C
		c.mu.Lock()
		if current := c.sessions[state]; current == session && current.status != "pending" {
			delete(c.sessions, state)
		}
		c.mu.Unlock()
	}()
}

func (c *oauthCoordinator) callbackHandler(session *oauthRunnerSession) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL == nil {
			writeOAuthBrowserPage(w, http.StatusNotFound, false)
			return
		}
		config := oauthProviders[session.provider]
		if r.URL.Path != config.callbackPath || len(r.URL.RawQuery) > oauthQueryLimit {
			writeOAuthBrowserPage(w, http.StatusBadRequest, false)
			return
		}
		state := strings.TrimSpace(r.URL.Query().Get("state"))
		code := strings.TrimSpace(r.URL.Query().Get("code"))
		errorCode := strings.TrimSpace(r.URL.Query().Get("error"))
		if state != session.state || (code == "" && errorCode == "") {
			writeOAuthBrowserPage(w, http.StatusBadRequest, false)
			return
		}
		c.mu.Lock()
		current := c.sessions[state]
		active := current == session && current.status == "pending" && time.Now().Before(current.expiresAt)
		c.mu.Unlock()
		if !active {
			writeOAuthBrowserPage(w, http.StatusGone, false)
			return
		}
		payload := map[string]string{"state": state}
		if code != "" {
			payload["code"] = code
		} else {
			payload["error"] = errorCode
		}
		var upstream map[string]any
		if err := c.upstreamJSON(r.Context(), http.MethodPost, "/v0/management/oauth-callback", payload, &upstream); err != nil {
			writeOAuthBrowserPage(w, http.StatusBadGateway, false)
			return
		}
		c.mu.Lock()
		c.closeListenerLocked(session)
		c.mu.Unlock()
		writeOAuthBrowserPage(w, http.StatusOK, true)
	})
}

func (c *oauthCoordinator) upstreamJSON(ctx context.Context, method, path string, src, dst any) (resultErr error) {
	var body io.Reader
	if src != nil {
		encoded, err := json.Marshal(src)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.managementKey)
	req.Header.Set("Accept", "application/json")
	if src != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		if err := response.Body.Close(); err != nil && resultErr == nil {
			resultErr = errors.New("invalid upstream response")
		}
	}()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New("upstream request failed")
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0]))
	if contentType != "application/json" {
		return errors.New("invalid upstream response")
	}
	limited, err := io.ReadAll(io.LimitReader(response.Body, oauthResponseLimit+1))
	if err != nil || len(limited) > oauthResponseLimit {
		return errors.New("invalid upstream response")
	}
	if dst != nil && json.Unmarshal(limited, dst) != nil {
		return errors.New("invalid upstream response")
	}
	return nil
}

func (c *oauthCoordinator) expireSession(state string, expiresAt time.Time) {
	timer := time.NewTimer(time.Until(expiresAt))
	defer timer.Stop()
	<-timer.C
	c.mu.Lock()
	defer c.mu.Unlock()
	if session := c.sessions[state]; session != nil && session.expiresAt.Equal(expiresAt) && session.status == "pending" {
		c.expireSessionLocked(session)
	}
}

func (c *oauthCoordinator) expireSessionLocked(session *oauthRunnerSession) {
	c.finishSessionLocked(session, "expired", "expired")
}

func (c *oauthCoordinator) closeListenerLocked(session *oauthRunnerSession) {
	if session != nil && session.listener != nil {
		_ = session.listener.Close()
		session.listener = nil
	}
}

func (c *oauthCoordinator) Close() {
	c.mu.Lock()
	c.closed = true
	for _, session := range c.sessions {
		if session.cancel != nil {
			session.cancel()
			session.cancel = nil
		}
		c.closeListenerLocked(session)
	}
	for subscriber := range c.subscribers {
		close(subscriber)
		delete(c.subscribers, subscriber)
	}
	c.mu.Unlock()
	c.workers.Wait()
}

func decodeBoundedJSON(body io.Reader, dst any) error {
	data, err := io.ReadAll(io.LimitReader(body, oauthResponseLimit+1))
	if err != nil || len(data) > oauthResponseLimit {
		return errors.New("invalid JSON")
	}
	return json.Unmarshal(data, dst)
}

func validAuthorizationURL(raw string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return "", false
	}
	return parsed.String(), true
}

func writeOAuthSession(w http.ResponseWriter, session *oauthRunnerSession) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"targetRef":        session.targetRef,
		"provider":         session.provider,
		"mode":             session.mode,
		"state":            session.state,
		"authorizationUrl": session.authorizationURL,
		"userCode":         session.userCode,
		"expiresAt":        session.expiresAt,
	})
}

func writeOAuthStatus(w http.ResponseWriter, status string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": status})
}

func writeOAuthError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func writeOAuthBrowserPage(w http.ResponseWriter, status int, success bool) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if success {
		_, _ = io.WriteString(w, "<!doctype html><title>Sign-in complete</title><p>Sign-in complete. You can close this window.</p>")
		return
	}
	_, _ = io.WriteString(w, "<!doctype html><title>Sign-in failed</title><p>Sign-in could not be completed. Return to AO and try again.</p>")
}
