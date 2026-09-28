package runner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestRunnerDurableMigrationAndIsolation(t *testing.T) {
	testRunnerDurableMigrationAndIsolation(t, false)
}

func TestRunnerDurableMigrationAndIsolationAfterModelRefresh(t *testing.T) {
	testRunnerDurableMigrationAndIsolation(t, true)
}

func testRunnerDurableMigrationAndIsolation(t *testing.T, afterModelRefresh bool) {
	if testing.Short() {
		t.Skip("runner subprocess integration")
	}
	keys := map[string]string{"a": "a-vault-secret", "b": "b-vault-secret"}
	var mu sync.Mutex
	seen := make(map[string]int)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet && request.URL.Path == "/v1/models" {
			for _, key := range keys {
				if request.Header.Get("Authorization") == "Bearer "+key {
					_, _ = io.WriteString(w, `{"data":[{"id":"test-model"}]}`)
					return
				}
			}
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(request.Body)
		name := ""
		for candidate := range keys {
			if bytes.Contains(body, []byte("session-request-"+candidate)) {
				name = candidate
			}
		}
		if request.URL.Path != "/v1/responses" || keys[name] == "" || request.Header.Get("Authorization") != "Bearer "+keys[name] {
			http.Error(w, "wrong selected account", http.StatusUnauthorized)
			return
		}
		mu.Lock()
		seen[name]++
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"selected\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"fixture\",\"status\":\"completed\",\"output\":[]}}\n\n")
	}))
	defer upstream.Close()
	stateDir, _ := validStateFixture(t)
	port := reservePort(t)
	configPath := filepath.Join(stateDir, configFileName)
	replaceInFile(t, configPath, "43127", strconv.Itoa(port))
	config, _ := os.ReadFile(configPath)
	writePrivateFile(t, configPath, string(config)+fmt.Sprintf("codex-api-key:\n  - api-key: %s\n    base-url: %s/v1\n  - api-key: %s\n    base-url: %s/v1\n", keys["a"], upstream.URL, keys["b"], upstream.URL))
	indexes := make(map[string]string)
	for name, key := range keys {
		auth := &coreauth.Auth{Provider: "codex", Attributes: map[string]string{"api_key": key, "base_url": upstream.URL + "/v1"}}
		indexes[name] = auth.EnsureIndex()
	}
	baseURL := "http://127.0.0.1:" + strconv.Itoa(port)
	logs := &lockedBuffer{}
	defer func() {
		if t.Failed() {
			t.Logf("runner diagnostics: %s", logs.String())
			entries, _ := os.ReadDir(filepath.Join(stateDir, authDirName))
			for _, entry := range entries {
				t.Logf("draft directory entry: %s (%s)", entry.Name(), entry.Type())
			}
		}
	}()
	var command *exec.Cmd
	stop := func() {
		if command != nil {
			_ = command.Process.Kill()
			_ = command.Wait()
			command = nil
		}
	}
	defer stop()
	start := func() {
		t.Helper()
		logOffset := len(logs.String())
		command = exec.Command(os.Args[0], "-test.run=^TestRunnerHelperProcess$")
		command.Env = append(os.Environ(), runnerHelperEnvironment+"=1", "AO_ACCOUNTS_MANAGER_TEST_STATE="+stateDir, "GIN_MODE=release", "GORACE=atexit_sleep_ms=0")
		if afterModelRefresh {
			command.Env = append(command.Env, "AO_ACCOUNTS_MANAGER_TEST_MODEL_BARRIER=1")
		}
		command.Stdout, command.Stderr = logs, logs
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		waitForRunnerHealth(t, baseURL)
		if afterModelRefresh {
			deadline := time.Now().Add(5 * time.Second)
			for !strings.Contains(logs.String()[logOffset:], modelRefreshBarrierMarker) {
				if time.Now().After(deadline) {
					t.Fatal("startup model refresh did not reach the registry barrier")
				}
				time.Sleep(time.Millisecond)
			}
		}
	}
	client := &http.Client{Timeout: 5 * time.Second}
	call := func(method, path, token string, body any) (int, []byte, error) {
		var encoded []byte
		if body != nil {
			encoded, _ = json.Marshal(body)
		}
		request, err := http.NewRequestWithContext(t.Context(), method, baseURL+path, bytes.NewReader(encoded))
		if err != nil {
			return 0, nil, err
		}
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			return 0, nil, err
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(response.Body)
		return response.StatusCode, raw, err
	}
	list := func(want int) {
		t.Helper()
		status, raw, err := call("GET", credentialPath, "management-secret", nil)
		var inventory struct {
			Files []credentialRecord `json:"files"`
		}
		if err != nil || status != 200 || json.Unmarshal(raw, &inventory) != nil || len(inventory.Files) != want {
			t.Fatalf("inventory status=%d count=%d want=%d err=%v", status, len(inventory.Files), want, err)
		}
		for _, record := range inventory.Files {
			if record.AuthIndex != indexes["a"] && record.AuthIndex != indexes["b"] {
				t.Fatal("migration changed account reference")
			}
		}
	}
	tokens := make(map[string]string)
	bindingSnapshot := routeBindingSnapshot{Revision: 1}
	for _, name := range []string{"a", "b"} {
		bindingSnapshot.Bindings = append(bindingSnapshot.Bindings, routeBinding{SessionID: "session-" + name, Provider: "codex", Mode: "managed", AccountID: publicCredentialID("management-secret", indexes[name]), Revision: 1})
	}
	synchronize := func() {
		if status, _, err := call("PUT", "/ao/internal/routes/bindings", "management-secret", bindingSnapshot); err != nil || status != 204 {
			t.Fatalf("reconcile status=%d err=%v", status, err)
		}
	}
	mint := func() {
		synchronize()
		for _, name := range []string{"a", "b"} {
			status, raw, err := call("POST", "/ao/internal/routes/token", "management-secret", map[string]any{"provider": "codex", "authIndex": indexes[name], "sessionId": "session-" + name, "accountId": publicCredentialID("management-secret", indexes[name]), "bindingRevision": 1})
			var route struct {
				Token string `json:"token"`
			}
			if err != nil || status != 200 || json.Unmarshal(raw, &route) != nil || route.Token == "" {
				t.Fatal("migrated route could not be minted")
			}
			tokens[name] = route.Token
		}
	}
	catalog, err := loadCredentialModels()
	if err != nil {
		t.Fatal(err)
	}
	model := catalog["codex"][0].ID
	stream := func(name string) error {
		status, raw, err := call("POST", "/v1/responses", tokens[name], map[string]any{"model": model, "input": "session-request-" + name, "stream": true})
		if err != nil {
			return err
		}
		if status != 200 || !bytes.Contains(raw, []byte("response.completed")) {
			diagnostic := string(raw)
			for _, key := range keys {
				diagnostic = strings.ReplaceAll(diagnostic, key, "[redacted]")
			}
			for _, token := range tokens {
				diagnostic = strings.ReplaceAll(diagnostic, token, "[redacted]")
			}
			if len(diagnostic) > 1024 {
				diagnostic = diagnostic[:1024]
			}
			return fmt.Errorf("account %s stream status=%d response=%q", name, status, diagnostic)
		}
		return nil
	}
	start()
	list(2)
	assertVaultHasNoSecret(t, stateDir)
	for _, name := range []string{"a", "b"} {
		status, _, err := call("POST", credentialPath+"/refresh?ref="+indexes[name], "management-secret", nil)
		if err != nil || status != http.StatusOK {
			t.Fatal("explicit verification of migrated credential failed")
		}
	}
	mint()
	results := make(chan error, 20)
	for i := 0; i < 10; i++ {
		for _, name := range []string{"a", "b"} {
			go func() { results <- stream(name) }()
		}
	}
	for i := 0; i < 20; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	stop()
	start()
	list(2)
	if err := stream("b"); err == nil {
		t.Fatal("restart admitted a token before reconciliation")
	}
	synchronize()
	if err := stream("b"); err != nil {
		t.Fatal("reconciliation did not restore an existing token")
	}
	mint()
	if err := stream("a"); err != nil {
		t.Fatal(err)
	}
	if err := stream("b"); err != nil {
		t.Fatal(err)
	}
	oldToken := tokens["a"]
	bindingSnapshot.Revision, bindingSnapshot.Bindings[0].Revision, bindingSnapshot.Bindings[0].AccountID = 2, 2, publicCredentialID("management-secret", indexes["b"])
	synchronize()
	if err := stream("a"); err == nil {
		t.Fatal("old session token survived a binding change")
	}
	status, raw, err := call("POST", "/ao/internal/routes/token", "management-secret", map[string]any{"provider": "codex", "authIndex": indexes["b"], "sessionId": "session-a", "accountId": publicCredentialID("management-secret", indexes["b"]), "bindingRevision": 2})
	var switched struct {
		Token string `json:"token"`
	}
	if err != nil || status != 200 || json.Unmarshal(raw, &switched) != nil || switched.Token == "" {
		t.Fatal("new session binding could not mint")
	}
	status, raw, err = call("POST", "/v1/responses", switched.Token, map[string]any{"model": model, "input": "session-request-b", "stream": true})
	if err != nil || status != 200 || !bytes.Contains(raw, []byte("response.completed")) {
		t.Fatal("new binding did not reach account B")
	}
	if err := stream("b"); err != nil {
		t.Fatal("switching session A affected session B")
	}
	bindingSnapshot.Revision, bindingSnapshot.Bindings[0].Revision, bindingSnapshot.Bindings[0].AccountID = 3, 3, publicCredentialID("management-secret", indexes["a"])
	synchronize()
	if status, _, _ := call("GET", "/v1/models", oldToken, nil); status == 200 {
		t.Fatal("switching back revived an old token")
	}
	status, raw, err = call("POST", "/ao/internal/routes/token", "management-secret", map[string]any{"provider": "codex", "authIndex": indexes["a"], "sessionId": "session-a", "accountId": publicCredentialID("management-secret", indexes["a"]), "bindingRevision": 3})
	if err != nil || status != 200 || json.Unmarshal(raw, &switched) != nil || switched.Token == "" {
		t.Fatal("restored selection could not mint")
	}
	tokens["a"] = switched.Token
	status, _, err = call("DELETE", credentialPath+"?ref="+indexes["a"], "management-secret", nil)
	if err != nil || status != 204 {
		t.Fatal("removal was not acknowledged")
	}
	if err := stream("a"); err == nil {
		t.Fatal("deleted account route still admitted")
	}
	if err := stream("b"); err != nil {
		t.Fatalf("removing account A interrupted account B: %v", err)
	}
	stop()
	start()
	list(1)
	synchronize()
	if err := stream("a"); err == nil {
		t.Fatal("restart resurrected account A")
	}
	if err := stream("b"); err != nil {
		t.Fatalf("restart after deletion interrupted account B: %v", err)
	}
	mu.Lock()
	counts := map[string]int{"a": seen["a"], "b": seen["b"]}
	mu.Unlock()
	if counts["a"] != 11 || counts["b"] != 16 {
		t.Fatalf("upstream counts=%v", counts)
	}
	for _, key := range keys {
		if strings.Contains(logs.String(), key) {
			t.Fatal("runner logs contain a credential")
		}
	}
	assertVaultHasNoSecret(t, stateDir)
	entries, err := os.ReadDir(filepath.Join(stateDir, authDirName))
	if err != nil || len(entries) != 0 {
		t.Fatal("runner wrote unexpected request or credential files")
	}
}
