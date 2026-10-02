package cli

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestManagedAccountsPublicCommands(t *testing.T) {
	for _, tt := range []struct {
		name, method, path, response, input string
		args                                []string
		body                                map[string]any
	}{
		{"inventory", "GET", "/accounts", `{"revision":8,"availability":"ready","stale":false,"accounts":[{"id":"amc_a","provider":"codex","generation":2,"privateHandle":"secret-marker"}],"oauthSessions":[{"id":"login-a","status":"pending","authorizationUrl":"secret-marker","userCode":"secret-marker"}]}`, "", []string{"ls"}, nil},
		{"refresh", "POST", "/accounts/amc_a/refresh", `{"revision":9,"availability":"ready","accounts":[{"id":"amc_a"}]}`, "", []string{"refresh", "amc_a"}, map[string]any{}},
		{"login", "POST", "/oauth-sessions", `{"id":"login-a","provider":"codex","mode":"device","status":"pending","authorizationUrl":"https://login.example/device","userCode":"ABCD","credential":"secret-marker"}`, "", []string{"login", "--provider", "codex", "--mode", "device"}, map[string]any{"provider": "codex", "mode": "device"}},
		{"reconnect", "POST", "/oauth-sessions", `{"id":"login-a","provider":"codex","mode":"callback","accountId":"amc_a","status":"pending"}`, "", []string{"login", "--provider", "codex", "--mode", "callback", "--account", "amc_a", "--generation", "2"}, map[string]any{"provider": "codex", "mode": "callback", "accountId": "amc_a", "generation": float64(2)}},
		{"cancel login", "DELETE", "/oauth-sessions/login-a", "", "", []string{"login-cancel", "login-a"}, nil},
		{"add key", "POST", "/accounts/api-key", `{"revision":9,"availability":"ready","accounts":[{"id":"amc_a","key":"secret-marker"}]}`, "secret-marker\n", []string{"add-key", "--provider", "codex", "--stdin", "--operation-id", "add-a"}, map[string]any{"provider": "codex", "operationId": "add-a", "key": "secret-marker"}},
		{"import", "POST", "/accounts/import", `{"revision":9,"availability":"ready","accounts":[{"id":"amc_a"}]}`, `{"access_token":"secret-marker"}`, []string{"import", "--provider", "codex", "--stdin", "--operation-id", "import-a"}, map[string]any{"provider": "codex", "operationId": "import-a", "filename": "credential.json", "credential": map[string]any{"access_token": "secret-marker"}}},
		{"disable", "PATCH", "/accounts/amc_a", `{"revision":9,"availability":"ready","accounts":[{"id":"amc_a","disabled":true}]}`, "", []string{"disable", "amc_a"}, map[string]any{"disabled": true}},
		{"enable", "PATCH", "/accounts/amc_a", `{"revision":9,"availability":"ready","accounts":[{"id":"amc_a","disabled":false}]}`, "", []string{"enable", "amc_a"}, map[string]any{"disabled": false}},
		{"rename", "PATCH", "/accounts/amc_a", `{"revision":9,"availability":"ready","accounts":[{"id":"amc_a","label":"Work"}]}`, "", []string{"rename", "amc_a", "Work", "--generation", "2"}, map[string]any{"label": "Work", "generation": float64(2)}},
		{"impact", "GET", "/accounts/amc_a/removal-impact", `{"accountId":"amc_a","revision":0,"sessions":[]}`, "", []string{"removal-impact", "amc_a"}, nil},
		{"remove zero", "POST", "/accounts/amc_a/removals", `{"id":"remove-a","accountId":"amc_a","phase":"requested","canCancel":true,"impact":{"accountId":"amc_a","revision":0,"sessions":[]}}`, "", []string{"remove", "amc_a", "--expected-revision", "0", "--operation-id", "remove-a", "--confirm"}, map[string]any{"expectedRevision": float64(0), "operationId": "remove-a", "confirmed": true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			capture := &agentSwitchRequestCapture{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/internal/") {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				capture.record(r)
				w.Header().Set("Content-Type", "application/json")
				if tt.response == "" {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				_, _ = io.WriteString(w, tt.response)
			}))
			t.Cleanup(server.Close)
			writeRunFileFor(t, cfg, server)
			args := append([]string{"accounts"}, tt.args...)
			args = append(args, "--json")
			out, errOut, err := executeCLI(t, Deps{In: strings.NewReader(tt.input), ProcessAlive: func(int) bool { return true }}, args...)
			if err != nil {
				t.Fatalf("public command missing: %v", err)
			}
			method, path, body, count := capture.snapshot()
			if count != 1 || method != tt.method || path != "/api/v1/accounts-manager"+tt.path {
				t.Fatalf("request=%s %s count=%d", method, path, count)
			}
			if tt.body != nil {
				var got map[string]any
				if json.Unmarshal(body, &got) != nil {
					t.Fatal("invalid request JSON")
				}
				want, _ := json.Marshal(tt.body)
				actual, _ := json.Marshal(got)
				if string(want) != string(actual) {
					t.Fatal("explicit request changed")
				}
			}
			if !json.Valid([]byte(out)) || strings.Contains(out+errOut, "secret-marker") {
				t.Fatal("invalid output or private data exposed")
			}
		})
	}
}

func TestManagedAccountsRequireExplicitIntent(t *testing.T) {
	for _, args := range [][]string{
		{"remove", "amc_a", "--confirm", "--operation-id", "remove-a"},
		{"remove", "amc_a", "--expected-revision", "0", "--operation-id", "remove-a"},
		{"remove", "amc_a", "--expected-revision", "0", "--confirm"},
		{"remove", "amc_a", "--expected-revision", "-1", "--operation-id", "remove-a", "--confirm"},
		{"remove", "amc_a", "--expected-revision", "9007199254740992", "--operation-id", "remove-a", "--confirm"},
		{"login", "--provider", "codex"},
		{"login", "--provider", "codex", "--mode", "callback", "--account", "amc_a"},
		{"login", "--provider", "codex", "--mode", "callback", "--generation", "2"},
		{"add-key", "--provider", "codex", "--operation-id", "add-a"},
		{"import", "--provider", "codex", "--stdin"},
		{"refresh", "../amc_a"},
		{"rename", "amc_a", "Work"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			cfg := setConfigEnv(t)
			capture := &agentSwitchRequestCapture{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capture.record(r)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			t.Cleanup(server.Close)
			writeRunFileFor(t, cfg, server)
			_, _, err := executeCLI(t, Deps{In: strings.NewReader("secret-marker"), ProcessAlive: func(int) bool { return true }}, append([]string{"accounts"}, args...)...)
			var usage usageError
			if !errors.As(err, &usage) {
				t.Fatalf("expected usage error, got %v", err)
			}
			_, _, _, count := capture.snapshot()
			if count != 0 {
				t.Fatalf("invalid input sent %d requests", count)
			}
		})
	}
}

func TestManagedAccountsRemovalRecovery(t *testing.T) {
	for _, action := range []string{"status", "retry", "cancel"} {
		for _, foreign := range []bool{false, true} {
			t.Run(action+map[bool]string{false: " same", true: " foreign"}[foreign], func(t *testing.T) {
				cfg := setConfigEnv(t)
				capture := &agentSwitchRequestCapture{}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasPrefix(r.URL.Path, "/internal/") {
						w.WriteHeader(http.StatusNoContent)
						return
					}
					capture.record(r)
					w.Header().Set("Content-Type", "application/json")
					account := "amc_a"
					if foreign {
						account = "amc_other"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"id": "remove-a", "accountId": account, "phase": "recovery_required", "impact": map[string]any{"accountId": account, "revision": 2, "sessions": []any{}}})
				}))
				t.Cleanup(server.Close)
				writeRunFileFor(t, cfg, server)
				out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "accounts", "removal-"+action, "amc_a", "remove-a", "--json")
				method, path, body, count := capture.snapshot()
				if foreign {
					if err == nil || out != "" || count != 1 || method != "GET" {
						t.Fatalf("foreign operation accepted: %v count=%d", err, count)
					}
					return
				}
				wantCount := 2
				wantMethod := "POST"
				wantPath := "/api/v1/accounts-manager/removals/remove-a/" + action
				if action == "status" {
					wantCount = 1
					wantMethod = "GET"
					wantPath = "/api/v1/accounts-manager/removals/remove-a"
				}
				if err != nil || count != wantCount || method != wantMethod || path != wantPath || !strings.Contains(out, "recovery_required") {
					t.Fatalf("recovery boundary: err=%v request=%s %s count=%d output=%s", err, method, path, count, out)
				}
				if action != "status" && strings.TrimSpace(string(body)) != "{}" {
					t.Fatal("retry retargeted account")
				}
			})
		}
	}
}

func TestManagedAccountsRedactErrorDetails(t *testing.T) {
	cfg := setConfigEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"code":"CONFLICT","message":"secret-marker at http://127.0.0.1:9988/private","requestId":"request-123"}`)
	}))
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)
	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "accounts", "refresh", "amc_a")
	if err == nil || !strings.Contains(err.Error(), "CONFLICT") || !strings.Contains(err.Error(), "request-123") {
		t.Fatalf("lost error correlation: %v", err)
	}
	if strings.Contains(out+errOut+err.Error(), "secret-marker") || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatal("private diagnostics exposed")
	}
}
