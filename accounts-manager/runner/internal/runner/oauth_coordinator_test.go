package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type runnerRoundTripFunc func(*http.Request) (*http.Response, error)

func (f runnerRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type inertListener struct {
	closed chan struct{}
	once   sync.Once
	addr   net.Addr
}

func newInertListener(address string) *inertListener {
	return &inertListener{closed: make(chan struct{}), addr: runnerTestAddr(address)}
}

func (l *inertListener) Accept() (net.Conn, error) { <-l.closed; return nil, net.ErrClosed }
func (l *inertListener) Close() error              { l.once.Do(func() { close(l.closed) }); return nil }
func (l *inertListener) Addr() net.Addr            { return l.addr }

type runnerTestAddr string

func (a runnerTestAddr) Network() string { return "tcp" }
func (a runnerTestAddr) String() string  { return string(a) }

func TestOAuthCoordinatorRequiresManagementKey(t *testing.T) {
	t.Parallel()

	coordinator := newOAuthCoordinator("http://127.0.0.1:12345", "management-key", &http.Client{}, nil)
	defer coordinator.Close()
	for _, token := range []string{"", "control-key", "data-plane-key"} {
		for _, request := range []*http.Request{
			httptest.NewRequest(http.MethodPost, "/ao/internal/oauth/start", strings.NewReader(`{"provider":"codex"}`)),
			httptest.NewRequest(http.MethodGet, "/ao/internal/oauth/status?state=opaque", nil),
			httptest.NewRequest(http.MethodGet, "/ao/internal/oauth/events", nil),
			httptest.NewRequest(http.MethodDelete, "/ao/internal/oauth/session?state=opaque", nil),
		} {
			if token != "" {
				request.Header.Set("Authorization", "Bearer "+token)
			}
			response := httptest.NewRecorder()
			coordinator.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("token=%q %s %s status = %d, want 401", token, request.Method, request.URL.Path, response.Code)
			}
		}
	}
}

func TestOAuthEventsReplayPendingThenPublishCompletionWithoutSecrets(t *testing.T) {
	t.Parallel()

	var complete atomic.Bool
	client := &http.Client{Transport: runnerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/v0/management/codex-auth-url":
			return jsonHTTPResponse(req, http.StatusOK, `{"status":"ok","url":"https://auth.example.test/private-start","state":"opaque-state"}`), nil
		case "/v0/management/get-auth-status":
			if complete.Load() {
				return jsonHTTPResponse(req, http.StatusOK, `{"status":"ok"}`), nil
			}
			return jsonHTTPResponse(req, http.StatusOK, `{"status":"wait"}`), nil
		default:
			return nil, errors.New("unexpected upstream request")
		}
	})}
	coordinator := newOAuthCoordinator("http://127.0.0.1:12345", "management-key", client, func(_, address string) (net.Listener, error) {
		return newInertListener(address), nil
	})
	coordinator.observeInterval = 5 * time.Millisecond
	coordinator.eventHeartbeatInterval = time.Second
	defer coordinator.Close()
	server := httptest.NewServer(coordinator)
	defer server.Close()

	startRequest, _ := http.NewRequest(http.MethodPost, server.URL+"/ao/internal/oauth/start", strings.NewReader(`{"provider":"codex"}`))
	startRequest.Header.Set("Authorization", "Bearer management-key")
	startRequest.Header.Set("Content-Type", "application/json")
	startResponse, err := server.Client().Do(startRequest)
	if err != nil {
		t.Fatal(err)
	}
	startResponse.Body.Close()
	if startResponse.StatusCode != http.StatusOK {
		t.Fatalf("start status = %d, want 200", startResponse.StatusCode)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eventsRequest, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/ao/internal/oauth/events", nil)
	eventsRequest.Header.Set("Authorization", "Bearer management-key")
	eventsResponse, err := server.Client().Do(eventsRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer eventsResponse.Body.Close()
	if eventsResponse.StatusCode != http.StatusOK {
		t.Fatalf("events status = %d, want 200", eventsResponse.StatusCode)
	}
	reader := bufio.NewReader(eventsResponse.Body)
	pending := readOAuthEventFrame(t, reader, time.Second)
	if pending.Status != "pending" || pending.Provider != "codex" || pending.State != "opaque-state" {
		t.Fatalf("pending event = %+v", pending)
	}

	complete.Store(true)
	completed := readOAuthEventFrame(t, reader, time.Second)
	if completed.Status != "completed" || completed.State != "opaque-state" {
		t.Fatalf("completed event = %+v", completed)
	}
	encoded, _ := json.Marshal(completed)
	for _, secret := range []string{"private-start", "management-key"} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatalf("event exposed %q", secret)
		}
	}
}

func TestCodexDeviceOAuthPublishesCodeAndCompletesWithoutUpstreamPolling(t *testing.T) {
	t.Parallel()

	done := make(chan error, 1)
	coordinator := newOAuthCoordinator("http://127.0.0.1:12345", "management-key", &http.Client{Transport: runnerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		t.Fatalf("device flow must not call callback management endpoints: %s", req.URL.Path)
		return nil, errors.New("unexpected request")
	})}, nil)
	coordinator.startCodexDevice = func(context.Context) (codexDeviceLogin, error) {
		return codexDeviceLogin{AuthorizationURL: codexDeviceVerificationURL, UserCode: "ABCD-EFGH", Done: done}, nil
	}
	defer coordinator.Close()

	request := httptest.NewRequest(http.MethodPost, "/ao/internal/oauth/start", strings.NewReader(`{"provider":"codex","mode":"device"}`))
	request.Header.Set("Authorization", "Bearer management-key")
	response := httptest.NewRecorder()
	coordinator.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("start status = %d body=%s", response.Code, response.Body.String())
	}
	var started struct {
		Mode             string `json:"mode"`
		AuthorizationURL string `json:"authorizationUrl"`
		UserCode         string `json:"userCode"`
		State            string `json:"state"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	if started.Mode != "device" || started.AuthorizationURL != codexDeviceVerificationURL || started.UserCode != "ABCD-EFGH" || started.State == "" {
		t.Fatalf("started = %#v", started)
	}

	done <- nil
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		coordinator.mu.Lock()
		status := coordinator.sessions[started.State].status
		coordinator.mu.Unlock()
		if status == "completed" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("device flow did not complete")
}

func TestCodexDeviceOAuthCancellationStopsLogin(t *testing.T) {
	t.Parallel()

	cancelled := make(chan struct{})
	coordinator := newOAuthCoordinator("http://127.0.0.1:12345", "management-key", &http.Client{}, nil)
	coordinator.startCodexDevice = func(ctx context.Context) (codexDeviceLogin, error) {
		done := make(chan error, 1)
		go func() {
			<-ctx.Done()
			close(cancelled)
			done <- ctx.Err()
		}()
		return codexDeviceLogin{AuthorizationURL: codexDeviceVerificationURL, UserCode: "ABCD-EFGH", Done: done}, nil
	}
	defer coordinator.Close()

	start := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ao/internal/oauth/start", strings.NewReader(`{"provider":"codex","mode":"device"}`))
	req.Header.Set("Authorization", "Bearer management-key")
	coordinator.ServeHTTP(start, req)
	var session struct {
		State string `json:"state"`
	}
	_ = json.Unmarshal(start.Body.Bytes(), &session)
	cancel := httptest.NewRecorder()
	cancelReq := httptest.NewRequest(http.MethodDelete, "/ao/internal/oauth/session?state="+session.State, nil)
	cancelReq.Header.Set("Authorization", "Bearer management-key")
	coordinator.ServeHTTP(cancel, cancelReq)
	if cancel.Code != http.StatusNoContent {
		t.Fatalf("cancel status = %d", cancel.Code)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("device login context was not cancelled")
	}
}

func TestOAuthEventsPublishCancellationAndExpiryAndHeartbeat(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: runnerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodDelete && req.URL.Path == "/v0/management/oauth-session" {
			return jsonHTTPResponse(req, http.StatusOK, `{"status":"ok","cancelled":true}`), nil
		}
		return jsonHTTPResponse(req, http.StatusOK, `{"status":"wait"}`), nil
	})}
	coordinator := newOAuthCoordinator("http://127.0.0.1:12345", "management-key", client, nil)
	coordinator.eventHeartbeatInterval = 10 * time.Millisecond
	defer coordinator.Close()
	now := time.Now()
	coordinator.sessions["cancel-state"] = &oauthRunnerSession{provider: "codex", state: "cancel-state", status: "pending", expiresAt: now.Add(time.Minute)}
	coordinator.providers["codex"] = "cancel-state"
	coordinator.sessions["expire-state"] = &oauthRunnerSession{provider: "claude", state: "expire-state", status: "pending", expiresAt: now.Add(25 * time.Millisecond)}
	coordinator.providers["claude"] = "expire-state"
	go coordinator.expireSession("expire-state", now.Add(25*time.Millisecond))

	server := httptest.NewServer(coordinator)
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/ao/internal/oauth/events", nil)
	request.Header.Set("Authorization", "Bearer management-key")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)

	replayed := map[string]string{}
	for len(replayed) < 2 {
		event := readOAuthEventFrame(t, reader, time.Second)
		replayed[event.State] = event.Status
	}
	if replayed["cancel-state"] != "pending" || replayed["expire-state"] != "pending" {
		t.Fatalf("initial replay = %#v", replayed)
	}

	cancelRequest, _ := http.NewRequest(http.MethodDelete, server.URL+"/ao/internal/oauth/session?state=cancel-state", nil)
	cancelRequest.Header.Set("Authorization", "Bearer management-key")
	cancelResponse, err := server.Client().Do(cancelRequest)
	if err != nil {
		t.Fatal(err)
	}
	cancelResponse.Body.Close()

	terminal := map[string]oauthEvent{}
	deadline := time.Now().Add(time.Second)
	for len(terminal) < 2 && time.Now().Before(deadline) {
		event := readOAuthEventFrame(t, reader, time.Until(deadline))
		if event.Status != "pending" {
			terminal[event.State] = event
		}
	}
	if terminal["cancel-state"].FailureCode != "cancelled" || terminal["expire-state"].Status != "expired" {
		t.Fatalf("terminal events = %#v", terminal)
	}
}

type oauthEvent struct {
	Provider    string    `json:"provider"`
	State       string    `json:"state"`
	Status      string    `json:"status"`
	FailureCode string    `json:"failureCode"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

func readOAuthEventFrame(t *testing.T, reader *bufio.Reader, timeout time.Duration) oauthEvent {
	t.Helper()
	type result struct {
		event oauthEvent
		err   error
	}
	resultCh := make(chan result, 1)
	go func() {
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				resultCh <- result{err: err}
				return
			}
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var event oauthEvent
			err = json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data: "))), &event)
			resultCh <- result{event: event, err: err}
			return
		}
	}()
	select {
	case got := <-resultCh:
		if got.err != nil {
			t.Fatal(got.err)
		}
		return got.event
	case <-time.After(timeout):
		t.Fatal("timed out waiting for OAuth event")
		return oauthEvent{}
	}
}

func TestOAuthCoordinatorBindsLoopbackBeforeRequestingAuthorizationURL(t *testing.T) {
	t.Parallel()

	var upstreamCalls atomic.Int32
	client := &http.Client{Transport: runnerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		upstreamCalls.Add(1)
		return jsonHTTPResponse(req, http.StatusOK, `{"status":"ok","url":"https://auth.example.test/start","state":"opaque-state"}`), nil
	})}
	var gotNetwork, gotAddress string
	listenErr := errors.New("port busy")
	coordinator := newOAuthCoordinator("http://127.0.0.1:12345", "management-key", client, func(network, address string) (net.Listener, error) {
		gotNetwork, gotAddress = network, address
		return nil, listenErr
	})
	defer coordinator.Close()

	request := httptest.NewRequest(http.MethodPost, "/ao/internal/oauth/start", strings.NewReader(`{"provider":"codex"}`))
	request.Header.Set("Authorization", "Bearer management-key")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	coordinator.ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", response.Code)
	}
	if gotNetwork != "tcp" || gotAddress != "127.0.0.1:1455" {
		t.Fatalf("listen = %s %s, want tcp 127.0.0.1:1455", gotNetwork, gotAddress)
	}
	if upstreamCalls.Load() != 0 {
		t.Fatalf("upstream calls = %d, want 0 before listener is ready", upstreamCalls.Load())
	}
	if strings.Contains(response.Body.String(), listenErr.Error()) {
		t.Fatal("response exposed listener error")
	}
}

func TestOAuthCoordinatorReusesPendingProviderAndAllowsOtherProvider(t *testing.T) {
	t.Parallel()

	var upstreamCalls atomic.Int32
	client := &http.Client{Transport: runnerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		call := upstreamCalls.Add(1)
		provider := "codex"
		if strings.Contains(req.URL.Path, "anthropic") {
			provider = "claude"
		}
		return jsonHTTPResponse(req, http.StatusOK, `{"status":"ok","url":"https://auth.example.test/`+provider+`","state":"state-`+provider+`-`+string('0'+call)+`"}`), nil
	})}
	var listenersMu sync.Mutex
	listeners := make([]*inertListener, 0, 2)
	coordinator := newOAuthCoordinator("http://127.0.0.1:12345", "management-key", client, func(_, address string) (net.Listener, error) {
		listener := newInertListener(address)
		listenersMu.Lock()
		listeners = append(listeners, listener)
		listenersMu.Unlock()
		return listener, nil
	})
	defer coordinator.Close()

	start := func(provider string) string {
		request := httptest.NewRequest(http.MethodPost, "/ao/internal/oauth/start", strings.NewReader(`{"provider":"`+provider+`"}`))
		request.Header.Set("Authorization", "Bearer management-key")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		coordinator.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("start %s status = %d body=%s", provider, response.Code, response.Body.String())
		}
		return response.Body.String()
	}

	firstCodex := start("codex")
	secondCodex := start("codex")
	if firstCodex != secondCodex {
		t.Fatalf("same-provider retry returned different session:\n%s\n%s", firstCodex, secondCodex)
	}
	_ = start("claude")
	if upstreamCalls.Load() != 2 {
		t.Fatalf("upstream start calls = %d, want 2", upstreamCalls.Load())
	}
	listenersMu.Lock()
	listenerCount := len(listeners)
	listenersMu.Unlock()
	if listenerCount != 2 {
		t.Fatalf("listeners = %d, want 2", listenerCount)
	}
}

func TestOAuthCallbackValidatesStateAndForwardsWithManagementAuthentication(t *testing.T) {
	t.Parallel()

	var forwarded atomic.Int32
	client := &http.Client{Transport: runnerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/v0/management/oauth-callback" || req.Method != http.MethodPost {
			t.Fatalf("unexpected upstream request: %s %s", req.Method, req.URL.Path)
		}
		if req.Header.Get("Authorization") != "Bearer management-key" {
			t.Fatal("callback was not forwarded with management authentication")
		}
		body, _ := io.ReadAll(req.Body)
		if !bytes.Contains(body, []byte(`"state":"opaque-state"`)) || !bytes.Contains(body, []byte(`"code":"private-code"`)) {
			t.Fatalf("callback body missing expected values: %s", body)
		}
		forwarded.Add(1)
		return jsonHTTPResponse(req, http.StatusOK, `{"status":"ok"}`), nil
	})}
	coordinator := newOAuthCoordinator("http://127.0.0.1:12345", "management-key", client, nil)
	defer coordinator.Close()
	session := &oauthRunnerSession{provider: "codex", state: "opaque-state", status: "pending", expiresAt: time.Now().Add(time.Minute)}
	coordinator.sessions[session.state] = session
	coordinator.providers[session.provider] = session.state

	wrong := httptest.NewRecorder()
	coordinator.callbackHandler(session).ServeHTTP(wrong, httptest.NewRequest(http.MethodGet, "/auth/callback?state=wrong&code=private-code", nil))
	if wrong.Code != http.StatusBadRequest || forwarded.Load() != 0 {
		t.Fatalf("wrong-state callback status=%d forwarded=%d", wrong.Code, forwarded.Load())
	}

	valid := httptest.NewRecorder()
	coordinator.callbackHandler(session).ServeHTTP(valid, httptest.NewRequest(http.MethodGet, "/auth/callback?state=opaque-state&code=private-code", nil))
	if valid.Code != http.StatusOK || forwarded.Load() != 1 {
		t.Fatalf("valid callback status=%d forwarded=%d", valid.Code, forwarded.Load())
	}
	for _, secret := range []string{"opaque-state", "private-code"} {
		if strings.Contains(valid.Body.String(), secret) {
			t.Fatalf("browser response exposed %q", secret)
		}
	}

	coordinator.mu.Lock()
	delete(coordinator.sessions, session.state)
	coordinator.mu.Unlock()
	late := httptest.NewRecorder()
	coordinator.callbackHandler(session).ServeHTTP(late, httptest.NewRequest(http.MethodGet, "/auth/callback?state=opaque-state&code=late-code", nil))
	if late.Code != http.StatusGone || forwarded.Load() != 1 {
		t.Fatalf("late callback status=%d forwarded=%d", late.Code, forwarded.Load())
	}
}

func TestOAuthCoordinatorStatusAndCancelAreSafeAndIdempotent(t *testing.T) {
	t.Parallel()

	var cancels atomic.Int32
	client := &http.Client{Transport: runnerRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/v0/management/get-auth-status":
			return jsonHTTPResponse(req, http.StatusOK, `{"status":"wait"}`), nil
		case req.Method == http.MethodDelete && req.URL.Path == "/v0/management/oauth-session":
			cancels.Add(1)
			return jsonHTTPResponse(req, http.StatusOK, `{"status":"ok","cancelled":true}`), nil
		default:
			return nil, errors.New("unexpected request")
		}
	})}
	coordinator := newOAuthCoordinator("http://127.0.0.1:12345", "management-key", client, nil)
	defer coordinator.Close()
	coordinator.sessions["opaque-state"] = &oauthRunnerSession{provider: "codex", state: "opaque-state", status: "pending", expiresAt: time.Now().Add(time.Minute)}
	coordinator.providers["codex"] = "opaque-state"

	statusRequest := httptest.NewRequest(http.MethodGet, "/ao/internal/oauth/status?state=opaque-state", nil)
	statusRequest.Header.Set("Authorization", "Bearer management-key")
	statusResponse := httptest.NewRecorder()
	coordinator.ServeHTTP(statusResponse, statusRequest)
	if statusResponse.Code != http.StatusOK || !strings.Contains(statusResponse.Body.String(), `"status":"pending"`) {
		t.Fatalf("status response = %d %s", statusResponse.Code, statusResponse.Body.String())
	}

	for range 2 {
		cancelRequest := httptest.NewRequest(http.MethodDelete, "/ao/internal/oauth/session?state=opaque-state", nil)
		cancelRequest.Header.Set("Authorization", "Bearer management-key")
		cancelResponse := httptest.NewRecorder()
		coordinator.ServeHTTP(cancelResponse, cancelRequest)
		if cancelResponse.Code != http.StatusNoContent {
			t.Fatalf("cancel status = %d, want 204", cancelResponse.Code)
		}
	}
	if cancels.Load() != 1 {
		t.Fatalf("upstream cancel calls = %d, want 1", cancels.Load())
	}
}

func jsonHTTPResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}
