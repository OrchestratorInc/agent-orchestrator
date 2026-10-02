package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const runnerHelperEnvironment = "AO_ACCOUNTS_MANAGER_RUNNER_HELPER"

func TestRunnerHelperProcess(t *testing.T) {
	if os.Getenv(runnerHelperEnvironment) != "1" {
		return
	}
	if os.Getenv("AO_ACCOUNTS_MANAGER_TEST_VERIFICATION") == "1" {
		original := http.DefaultTransport
		http.DefaultTransport = runnerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.Host == "chatgpt.com" && request.URL.Path == "/backend-api/wham/usage" || request.URL.Host == "api.anthropic.com" && request.URL.Path == "/api/oauth/profile" {
				return successfulCredentialCheck(request)
			}
			return original.RoundTrip(request)
		})
	}
	if os.Getenv("AO_ACCOUNTS_MANAGER_TEST_MODEL_BARRIER") != "" {
		runWithModelRefreshBarrier(t)
		return
	}
	code := RunCLI(context.Background(), []string{"serve", "--state-dir", os.Getenv("AO_ACCOUNTS_MANAGER_TEST_STATE")}, os.Stdout, os.Stderr)
	os.Exit(code)
}

func TestRunnerStreamsThroughSelectedAccountWithoutLeakingSecrets(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping runner integration in short mode")
	}

	const (
		controlKey    = "control-secret-do-not-log"
		clientKey     = "client-secret-do-not-log"
		managementKey = "management-secret-do-not-log"
		providerKey   = "provider-secret-do-not-log"
		requestSecret = "request-body-secret-do-not-log"
	)
	upstreamRequest := make(chan []byte, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/models" && r.Header.Get("Authorization") == "Bearer "+providerKey {
			_, _ = io.WriteString(w, `{"data":[{"id":"test-model"}]}`)
			return
		}
		if r.URL.Path != "/v1/responses" {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+providerKey {
			http.Error(w, "unexpected provider credential", http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		upstreamRequest <- body
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello \"}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"world\"}\n\n")
		flusher.Flush()
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"test-response\",\"status\":\"completed\",\"output\":[]}}\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	runnerPort := reservePort(t)
	stateDir := t.TempDir()
	chmodPrivateDir(t, stateDir)
	authDir := filepath.Join(stateDir, "auth")
	if err := os.Mkdir(authDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writePrivateFile(t, filepath.Join(stateDir, "control.key"), controlKey+"\n")
	writePrivateFile(t, filepath.Join(stateDir, "management.key"), managementKey+"\n")
	writePrivateFile(t, filepath.Join(stateDir, "routing.key"), strings.Repeat("11", 32)+"\n")
	config := fmt.Sprintf(`host: 127.0.0.1
port: %d
auth-dir: %s
api-keys:
  - %s
request-log: false
logging-to-file: false
usage-statistics-enabled: false
remote-management:
  allow-remote: false
  secret-key: ''
  disable-control-panel: true
  disable-auto-update-panel: true
plugins:
  enabled: false
pprof:
  enable: false
discovery:
  enabled: false
`, runnerPort, authDir, clientKey)
	writePrivateFile(t, filepath.Join(stateDir, "config.yaml"), config)

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	logs := &lockedBuffer{}
	command := exec.Command(executable, "-test.run=^TestRunnerHelperProcess$")
	command.Env = append(os.Environ(), runnerHelperEnvironment+"=1", "AO_ACCOUNTS_MANAGER_TEST_STATE="+stateDir, "GIN_MODE=release")
	command.Stdout = logs
	command.Stderr = logs
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	}()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", runnerPort)
	waitForRunnerHealth(t, baseURL)

	runtimeBytes, err := os.ReadFile(filepath.Join(stateDir, "runtime.json"))
	if err != nil {
		t.Fatal(err)
	}
	var runtimeRecord RuntimeRecord
	if err = json.Unmarshal(runtimeBytes, &runtimeRecord); err != nil {
		t.Fatal(err)
	}
	identityRequest, _ := http.NewRequest(http.MethodGet, baseURL+"/ao/internal/identity", nil)
	identityRequest.Header.Set("Authorization", "Bearer "+controlKey)
	identityResponse, err := http.DefaultClient.Do(identityRequest)
	if err != nil {
		t.Fatal(err)
	}
	identityBody, _ := io.ReadAll(identityResponse.Body)
	_ = identityResponse.Body.Close()
	if identityResponse.StatusCode != http.StatusOK || !bytes.Contains(identityBody, []byte(runtimeRecord.InstanceID)) {
		t.Fatalf("identity status=%d body=%s", identityResponse.StatusCode, identityBody)
	}

	createBody, _ := json.Marshal(map[string]string{"operationId": "stream-key", "provider": "codex", "key": providerKey, "baseUrl": upstream.URL + "/v1"})
	create, _ := http.NewRequest(http.MethodPost, baseURL+credentialPath+"/api-key", bytes.NewReader(createBody))
	create.Header.Set("Authorization", "Bearer "+managementKey)
	createResponse, err := http.DefaultClient.Do(create)
	if err != nil {
		t.Fatal(err)
	}
	var credential credentialRecord
	err = json.NewDecoder(createResponse.Body).Decode(&credential)
	_ = createResponse.Body.Close()
	if err != nil || createResponse.StatusCode != http.StatusOK || credential.AuthIndex == "" {
		t.Fatalf("encrypted create status=%d err=%v", createResponse.StatusCode, err)
	}
	catalog, err := loadCredentialModels()
	if err != nil {
		t.Fatal(err)
	}
	model := catalog["codex"][0].ID
	accountID := publicCredentialID(managementKey, credential.AuthIndex)
	bindingBody, _ := json.Marshal(routeBindingSnapshot{Revision: 1, Bindings: []routeBinding{{SessionID: "stream-test", Provider: "codex", Mode: "managed", AccountID: accountID, Revision: 1}}})
	syncBindings, _ := http.NewRequest(http.MethodPut, baseURL+"/ao/internal/routes/bindings", bytes.NewReader(bindingBody))
	syncBindings.Header.Set("Authorization", "Bearer "+managementKey)
	syncResponse, err := http.DefaultClient.Do(syncBindings)
	if err != nil {
		t.Fatal(err)
	}
	_ = syncResponse.Body.Close()
	if syncResponse.StatusCode != http.StatusNoContent {
		t.Fatalf("binding status=%d", syncResponse.StatusCode)
	}
	mintBody, _ := json.Marshal(map[string]any{"provider": "codex", "authIndex": credential.AuthIndex, "sessionId": "stream-test", "accountId": accountID, "bindingRevision": 1})
	mint, _ := http.NewRequest(http.MethodPost, baseURL+"/ao/internal/routes/token", bytes.NewReader(mintBody))
	mint.Header.Set("Authorization", "Bearer "+managementKey)
	mintResponse, err := http.DefaultClient.Do(mint)
	if err != nil {
		t.Fatal(err)
	}
	var route struct {
		Token string `json:"token"`
	}
	err = json.NewDecoder(mintResponse.Body).Decode(&route)
	_ = mintResponse.Body.Close()
	if err != nil || mintResponse.StatusCode != http.StatusOK || route.Token == "" {
		t.Fatalf("mint status=%d err=%v", mintResponse.StatusCode, err)
	}
	requestBody := fmt.Sprintf(`{"model":%q,"input":"%s","stream":true}`, model, requestSecret)
	request, _ := http.NewRequest(http.MethodPost, baseURL+"/v1/responses", strings.NewReader(requestBody))
	request.Header.Set("Authorization", "Bearer "+route.Token)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	stream, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("proxy status=%d body=%s logs=%s", response.StatusCode, stream, logs.String())
	}
	for _, chunk := range []string{"hello", "world", "response.completed"} {
		if !bytes.Contains(stream, []byte(chunk)) {
			t.Fatalf("stream missing %q: %s", chunk, stream)
		}
	}

	select {
	case got := <-upstreamRequest:
		if !bytes.Contains(got, []byte(requestSecret)) {
			t.Fatalf("upstream request did not receive request content: %s", got)
		}
	case <-time.After(time.Second):
		t.Fatal("fake provider did not receive the request")
	}

	combinedPublicOutput := strings.Join([]string{string(runtimeBytes), string(identityBody), logs.String()}, "\n")
	for _, secret := range []string{controlKey, clientKey, managementKey, providerKey, requestSecret, route.Token} {
		if strings.Contains(combinedPublicOutput, secret) {
			t.Fatalf("runner exposed secret %q", secret)
		}
	}
}

func TestServeRequiresManagementKeyForManagementAPI(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping runner integration in short mode")
	}

	const (
		controlKey    = "management-test-control"
		clientKey     = "management-test-client"
		managementKey = "management-test-secret"
	)
	runnerPort := reservePort(t)
	stateDir := t.TempDir()
	chmodPrivateDir(t, stateDir)
	authDir := filepath.Join(stateDir, "auth")
	if err := os.Mkdir(authDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writePrivateFile(t, filepath.Join(stateDir, "control.key"), controlKey+"\n")
	writePrivateFile(t, filepath.Join(stateDir, "management.key"), managementKey+"\n")
	writePrivateFile(t, filepath.Join(stateDir, "routing.key"), strings.Repeat("22", 32)+"\n")
	config := fmt.Sprintf(`host: 127.0.0.1
port: %d
auth-dir: %s
api-keys:
  - %s
request-log: false
logging-to-file: false
usage-statistics-enabled: false
remote-management:
  allow-remote: false
  secret-key: ''
  disable-control-panel: true
  disable-auto-update-panel: true
plugins:
  enabled: false
pprof:
  enable: false
discovery:
  enabled: false
routing:
  strategy: round-robin
`, runnerPort, authDir, clientKey)
	writePrivateFile(t, filepath.Join(stateDir, "config.yaml"), config)

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	logs := &lockedBuffer{}
	command := exec.Command(executable, "-test.run=^TestRunnerHelperProcess$")
	command.Env = append(os.Environ(), runnerHelperEnvironment+"=1", "AO_ACCOUNTS_MANAGER_TEST_STATE="+stateDir, "GIN_MODE=release")
	command.Env = append(command.Env, "AO_ACCOUNTS_MANAGER_TEST_VERIFICATION=1")
	command.Stdout = logs
	command.Stderr = logs
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	}()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", runnerPort)
	waitForRunnerHealth(t, baseURL)
	managementURL := baseURL + "/v0/management/routing/strategy"
	for _, path := range []string{"/v0/management/config.yaml", "/v0/management/request-log", "/v0/management/plugins"} {
		request, _ := http.NewRequest(http.MethodGet, baseURL+path, nil)
		request.Header.Set("Authorization", "Bearer "+managementKey)
		response, requestErr := http.DefaultClient.Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusForbidden {
			t.Errorf("unsupported management route %s returned %d", path, response.StatusCode)
		}
	}
	callback, _ := http.NewRequest(http.MethodPost, baseURL+"/v0/management/oauth-callback", strings.NewReader(`{"state":"unknown","code":"example"}`))
	callbackResponse, callbackErr := http.DefaultClient.Do(callback)
	if callbackErr != nil {
		t.Fatal(callbackErr)
	}
	_ = callbackResponse.Body.Close()
	if callbackResponse.StatusCode != http.StatusUnauthorized {
		t.Errorf("unauthenticated callback status = %d", callbackResponse.StatusCode)
	}

	for _, tt := range []struct {
		name  string
		token string
		want  int
	}{
		{name: "no token", want: http.StatusUnauthorized},
		{name: "lifecycle key", token: controlKey, want: http.StatusUnauthorized},
		{name: "data plane key", token: clientKey, want: http.StatusUnauthorized},
		{name: "management key", token: managementKey, want: http.StatusOK},
	} {
		t.Run(tt.name, func(t *testing.T) {
			request, requestErr := http.NewRequest(http.MethodGet, managementURL, nil)
			if requestErr != nil {
				t.Fatal(requestErr)
			}
			if tt.token != "" {
				request.Header.Set("Authorization", "Bearer "+tt.token)
			}
			response, requestErr := http.DefaultClient.Do(request)
			if requestErr != nil {
				t.Fatal(requestErr)
			}
			defer response.Body.Close()
			if response.StatusCode != tt.want {
				t.Fatalf("management status = %d, want %d", response.StatusCode, tt.want)
			}
			if tt.want == http.StatusOK {
				var payload struct {
					Strategy string `json:"strategy"`
				}
				if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				if payload.Strategy != "round-robin" && payload.Strategy != "weighted-round-robin" && payload.Strategy != "fill-first" {
					t.Fatalf("unexpected routing strategy %q", payload.Strategy)
				}
			}
		})
	}

	authFilesRequest, err := http.NewRequest(http.MethodGet, baseURL+credentialPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	authFilesRequest.Header.Set("Authorization", "Bearer "+managementKey)
	authFilesResponse, err := http.DefaultClient.Do(authFilesRequest)
	if err != nil {
		t.Fatal(err)
	}
	var authFilesPayload struct {
		Files []json.RawMessage `json:"files"`
	}
	if err = json.NewDecoder(authFilesResponse.Body).Decode(&authFilesPayload); err != nil {
		_ = authFilesResponse.Body.Close()
		t.Fatal(err)
	}
	_ = authFilesResponse.Body.Close()
	if authFilesResponse.StatusCode != http.StatusOK || len(authFilesPayload.Files) != 0 {
		t.Fatalf("empty credential inventory status=%d count=%d", authFilesResponse.StatusCode, len(authFilesPayload.Files))
	}

	const importedCredentialSecret = "runner-import-secret-do-not-log"
	for _, fixture := range []struct {
		name     string
		provider string
	}{
		{name: "fake-codex.json", provider: "codex"},
		{name: "fake-claude.json", provider: "claude"},
	} {
		body := fmt.Sprintf(`{"operationId":%q,"provider":%q,"credential":{"type":%q,"email":%q,"access_token":%q}}`, "import-"+fixture.provider, fixture.provider, fixture.provider, fixture.provider+"@example.test", importedCredentialSecret+"-"+fixture.provider)
		request, requestErr := http.NewRequest(http.MethodPost, baseURL+credentialPath+"/import", strings.NewReader(body))
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		request.Header.Set("Authorization", "Bearer "+managementKey)
		request.Header.Set("Content-Type", "application/json")
		response, requestErr := http.DefaultClient.Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		responseBody, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("upload %s status = %d, response=%s", fixture.provider, response.StatusCode, responseBody)
		}
	}

	type listedCredential struct {
		AuthIndex string `json:"auth_index"`
		Name      string `json:"name"`
		Provider  string `json:"provider"`
		Disabled  bool   `json:"disabled"`
	}
	listCredentials := func() []listedCredential {
		t.Helper()
		request, requestErr := http.NewRequest(http.MethodGet, baseURL+credentialPath, nil)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		request.Header.Set("Authorization", "Bearer "+managementKey)
		response, requestErr := http.DefaultClient.Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		defer response.Body.Close()
		var payload struct {
			Files []listedCredential `json:"files"`
		}
		if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&payload) != nil {
			t.Fatalf("list credentials status = %d", response.StatusCode)
		}
		return payload.Files
	}
	var imported []listedCredential
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		imported = listCredentials()
		if len(imported) == 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(imported) != 2 {
		t.Fatalf("imported credential count = %d, want 2", len(imported))
	}
	for _, credential := range imported {
		if credential.AuthIndex == "" || (credential.Provider != "codex" && credential.Provider != "claude") {
			t.Fatalf("unsafe imported credential projection: %+v", credential)
		}
	}

	target := imported[0]
	for _, disabled := range []bool{true, false} {
		body, _ := json.Marshal(map[string]any{"disabled": disabled})
		request, _ := http.NewRequest(http.MethodPatch, baseURL+credentialPath+"/status?ref="+url.QueryEscape(target.AuthIndex), bytes.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+managementKey)
		request.Header.Set("Content-Type", "application/json")
		response, requestErr := http.DefaultClient.Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusNoContent {
			t.Fatalf("set disabled=%t status = %d", disabled, response.StatusCode)
		}
		found := false
		for _, credential := range listCredentials() {
			if credential.AuthIndex == target.AuthIndex {
				found = true
				if credential.Disabled != disabled {
					t.Fatalf("disabled projection = %t, want %t", credential.Disabled, disabled)
				}
			}
		}
		if !found {
			t.Fatal("updated credential disappeared")
		}
	}

	refreshRequest, _ := http.NewRequest(http.MethodPost, baseURL+credentialPath+"/refresh?ref="+url.QueryEscape(target.AuthIndex), nil)
	refreshRequest.Header.Set("Authorization", "Bearer "+managementKey)
	refreshRequest.Header.Set("Content-Type", "application/json")
	refreshResponse, err := http.DefaultClient.Do(refreshRequest)
	if err != nil {
		t.Fatal(err)
	}
	_ = refreshResponse.Body.Close()
	if refreshResponse.StatusCode != http.StatusOK {
		t.Fatalf("refresh status = %d", refreshResponse.StatusCode)
	}

	for _, credential := range imported {
		request, _ := http.NewRequest(http.MethodDelete, baseURL+credentialPath+"?ref="+url.QueryEscape(credential.AuthIndex), nil)
		request.Header.Set("Authorization", "Bearer "+managementKey)
		response, requestErr := http.DefaultClient.Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusNoContent {
			t.Fatalf("delete %s status = %d", credential.Provider, response.StatusCode)
		}
	}
	if remaining := listCredentials(); len(remaining) != 0 {
		t.Fatalf("credentials after removal = %+v", remaining)
	}

	renewCtx, stopRenewing := context.WithCancel(context.Background())
	renewed := make(chan struct{}, 1)
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			request, requestErr := http.NewRequestWithContext(renewCtx, http.MethodPost, baseURL+"/ao/internal/lease", nil)
			if requestErr == nil {
				request.Header.Set("Authorization", "Bearer "+controlKey)
				if response, requestErr := http.DefaultClient.Do(request); requestErr == nil {
					_ = response.Body.Close()
					if response.StatusCode == http.StatusNoContent {
						select {
						case renewed <- struct{}{}:
						default:
						}
					}
				}
			}
			select {
			case <-renewCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	select {
	case <-renewed:
		stopRenewing()
	case <-time.After(time.Second):
		stopRenewing()
		t.Fatal("daemon-side lease renewal did not reach the runner")
	}
	time.Sleep(100 * time.Millisecond)
	healthResponse, err := http.Get(baseURL + "/healthz")
	if err != nil {
		t.Fatalf("runner was not available during the reattach lease window: %v", err)
	}
	_ = healthResponse.Body.Close()
	if healthResponse.StatusCode != http.StatusOK {
		t.Fatalf("runner health after daemon-side stop = %d, want 200", healthResponse.StatusCode)
	}

	response, err := http.Get(baseURL + "/management.html")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("management panel status = %d, want %d", response.StatusCode, http.StatusNotFound)
	}
	if logsText := logs.String(); strings.Contains(logsText, controlKey) || strings.Contains(logsText, clientKey) || strings.Contains(logsText, managementKey) || strings.Contains(logsText, importedCredentialSecret) {
		t.Fatal("runner logs exposed a private key")
	}
}

func reservePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	return port
}

func waitForRunnerHealth(t *testing.T, baseURL string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get(baseURL + "/healthz")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("runner did not become healthy")
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}
