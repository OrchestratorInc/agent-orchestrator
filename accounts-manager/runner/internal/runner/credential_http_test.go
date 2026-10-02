package runner

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestCredentialHTTPDurableLifecycle(t *testing.T) {
	vault := newTestVault(t)
	models, err := loadCredentialModels()
	if err != nil {
		t.Fatal(err)
	}
	runtime := &credentialRuntime{vault: vault, manager: coreauth.NewManager(vault, nil, nil), models: models, checkTransport: runnerRoundTripFunc(successfulCredentialCheck)}
	handler := &credentialHTTP{key: "management", runtime: runtime}
	call := func(method, path, body string, want int) []byte {
		t.Helper()
		request := httptest.NewRequest(method, credentialPath+path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer management")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("%s %s: status=%d want=%d body=%s", method, path, response.Code, want, response.Body.String())
		}
		if strings.Contains(response.Body.String(), "fixture-private-key") {
			t.Fatal("response exposed key")
		}
		return response.Body.Bytes()
	}
	payload := `{"operationId":"connect-a","provider":"codex","key":"fixture-private-key","baseUrl":"http://127.0.0.1:1/v1"}`
	var record credentialRecord
	if json.Unmarshal(call("POST", "/api-key", payload, 200), &record) != nil || record.AuthIndex == "" || record.AccountType != "api_key" {
		t.Fatal("missing safe credential record")
	}
	var repeated credentialRecord
	_ = json.Unmarshal(call("POST", "/api-key", payload, 200), &repeated)
	if repeated.AuthIndex != record.AuthIndex || len(runtime.manager.List()) != 1 {
		t.Fatal("retry duplicated credential")
	}
	call("POST", "/api-key", strings.Replace(payload, "fixture-private-key", "other-key", 1), 409)
	ref := "?ref=" + record.AuthIndex
	auth := runtime.manager.List()[0]
	if len(cliproxy.GlobalModelRegistry().GetModelsForClient(auth.ID)) == 0 {
		t.Fatal("committed credential has no models")
	}
	call("PATCH", "/status"+ref, `{"disabled":true}`, 204)
	if vault.Admit(t.Context(), auth) || len(cliproxy.GlobalModelRegistry().GetModelsForClient(auth.ID)) != 0 {
		t.Fatal("disabled credential remains admitted")
	}
	call("PATCH", "/status"+ref, `{"disabled":false}`, 204)
	if vault.Admit(t.Context(), auth) {
		t.Fatal("re-enabled stale generation admitted")
	}
	if err := runtime.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	call("GET", "/models"+ref, "", 200)
	call("POST", "/refresh"+ref, "", 200)
	call("DELETE", ref, "", 204)
	call("DELETE", ref, "", 204)
	call("POST", "/api-key", payload, 409)
	if len(runtime.manager.List()) != 0 || len(cliproxy.GlobalModelRegistry().GetModelsForClient(auth.ID)) != 0 {
		t.Fatal("removal retained runtime state")
	}
	if err := filepath.WalkDir(vault.root.Name(), func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if bytes.Contains(data, []byte("fixture-private-key")) {
			t.Fatal("plaintext credential on disk")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	_ = vault.Close()
	call("DELETE", ref, "", 503)
}

func TestCredentialImportSDKFields(t *testing.T) {
	auth, err := parseCredentialCreate(credentialCreate{Provider: "claude", Credential: json.RawMessage(`{"type":"claude","access_token":"fixture-token","account_uuid":"account","organization_uuid":"org","organization_name":"Work","claude_device_ids":["device"],"disabled":true}`)}, false)
	if err != nil || !auth.Disabled || auth.Metadata["account_uuid"] != "account" || auth.Metadata["organization_uuid"] != "org" {
		t.Fatal("SDK account identity was not preserved")
	}
	for _, raw := range []string{`["one","two"]`, `"device"`, `["bad\nvalue"]`} {
		_, err := parseCredentialCreate(credentialCreate{Provider: "claude", Credential: json.RawMessage(`{"type":"claude","access_token":"fixture-token","claude_device_ids":` + raw + `}`)}, false)
		if err == nil {
			t.Fatal("invalid device identity imported")
		}
	}
}

func TestCredentialDisabledImport(t *testing.T) {
	vault := newTestVault(t)
	runtime := &credentialRuntime{vault: vault, manager: coreauth.NewManager(vault, nil, nil), checkTransport: runnerRoundTripFunc(successfulCredentialCheck)}
	handler := &credentialHTTP{key: "management", runtime: runtime}
	request := httptest.NewRequest(http.MethodPost, credentialPath+"/import", strings.NewReader(`{"operationId":"disabled-import","provider":"codex","credential":{"type":"codex","access_token":"token-vault-secret","disabled":true}}`))
	request.Header.Set("Authorization", "Bearer management")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 200 || len(runtime.manager.List()) != 1 {
		t.Fatal("disabled import was not acknowledged")
	}
	auth := runtime.manager.List()[0]
	if !auth.Disabled || vault.Admit(t.Context(), auth) {
		t.Fatal("disabled import became eligible for requests")
	}
}

func TestCredentialHTTPRejectsUnsafeInput(t *testing.T) {
	vault := newTestVault(t)
	runtime := &credentialRuntime{vault: vault, manager: coreauth.NewManager(vault, nil, nil)}
	handler := &credentialHTTP{key: "management", runtime: runtime}
	for _, test := range []struct {
		name, path, body, key string
		status                int
	}{
		{"unauthorized", "/api-key", `{}`, "", 401},
		{"unknown field", "/api-key", `{"operationId":"a","provider":"codex","key":"key","path":"outside"}`, "management", 409},
		{"header injection", "/api-key", `{"operationId":"a","provider":"codex","key":"key\r\nInjected: secret"}`, "management", 409},
		{"remote cleartext", "/api-key", `{"operationId":"a","provider":"codex","key":"key","baseUrl":"http://example.com"}`, "management", 409},
		{"trailing object", "/api-key", `{"operationId":"a","provider":"codex","key":"key"}{}`, "management", 409},
		{"oversized", "/import", strings.Repeat(" ", (1<<20)+1), "management", 409},
		{"import path", "/import", `{"operationId":"a","provider":"codex","credential":{"type":"codex","access_token":"secret","path":"outside"}}`, "management", 409},
		{"wrong provider", "/import", `{"operationId":"a","provider":"codex","credential":{"type":"other","access_token":"secret"}}`, "management", 409},
		{"missing token", "/import", `{"operationId":"a","provider":"codex","credential":{"type":"codex"}}`, "management", 409},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, credentialPath+test.path, strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer "+test.key)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d want=%d", response.Code, test.status)
			}
		})
	}
	if len(runtime.manager.List()) != 0 {
		t.Fatal("invalid input published a credential")
	}
}
