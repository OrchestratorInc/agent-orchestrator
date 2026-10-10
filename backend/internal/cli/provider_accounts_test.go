package cli

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestProviderAccountSpawnCLIPreservesSelectionAndSessionOptions(t *testing.T) {
	cases := []struct {
		name                                                     string
		args                                                     []string
		wantAccount, wantAgent, wantMode, wantModel, wantProject string
	}{
		{name: "codex-default", args: []string{"--project", "demo", "--agent", "codex"}, wantAgent: "codex", wantProject: "demo"},
		{name: "claude-default", args: []string{"--project", "demo", "--agent", "claude-code"}, wantAgent: "claude-code", wantProject: "demo"},
		{name: "explicit-codex", args: []string{"--project", "demo", "--agent", "codex", "--account", "account-a"}, wantAgent: "codex", wantProject: "demo", wantAccount: "account-a"},
		{name: "explicit-claude", args: []string{"--project", "demo", "--agent", "claude-code", "--account", "account-b"}, wantAgent: "claude-code", wantProject: "demo", wantAccount: "account-b"},
		{name: "codex-chat", args: []string{"--project", "demo", "--agent", "codex", "--account", "account-a", "--mode", "chat"}, wantAgent: "codex", wantProject: "demo", wantAccount: "account-a", wantMode: "chat"},
		{name: "claude-tui", args: []string{"--project", "demo", "--agent", "claude-code", "--account", "account-b", "--mode", "tui"}, wantAgent: "claude-code", wantProject: "demo", wantAccount: "account-b", wantMode: "tui"},
		{name: "model-override", args: []string{"--project", "demo", "--agent", "codex", "--account", "account-a", "--model", "model-test"}, wantAgent: "codex", wantProject: "demo", wantAccount: "account-a", wantModel: "model-test"},
		{name: "trimmed-selection", args: []string{"--project", "demo", "--agent", "codex", "--account", "  account-a  "}, wantAgent: "codex", wantProject: "demo", wantAccount: "account-a"},
		{name: "blank-selection", args: []string{"--project", "demo", "--agent", "codex", "--account", "  "}, wantAgent: "codex", wantProject: "demo"},
		{name: "standalone-codex", args: []string{"--standalone", "--agent", "codex", "--account", "account-a"}, wantAgent: "codex", wantAccount: "account-a"},
		{name: "standalone-claude", args: []string{"--standalone", "--agent", "claude-code", "--account", "account-b", "--mode", "chat"}, wantAgent: "claude-code", wantAccount: "account-b", wantMode: "chat"},
		{name: "inherited-project-agent", args: []string{"--project", "demo", "--account", "account-a"}, wantAgent: "codex", wantProject: "demo", wantAccount: "account-a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			t.Setenv("AO_SESSION_ID", "")
			var requests []string
			var received map[string]json.RawMessage
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				appendPrimaryRequest(&requests, r)
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/v1/projects/demo":
					_, _ = io.WriteString(w, `{"project":{"id":"demo","name":"Demo","path":"/repo/demo","config":{"worker":{"agent":"codex"}}}}`)
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/agents/readiness/ensure":
					_, _ = io.WriteString(w, authorizedAgentsJSON(tc.wantAgent))
				case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
					if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
						t.Error(err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					_, _ = io.WriteString(w, `{"session":{"id":"new-session","status":"idle","displayName":"account worker"}}`)
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)
			writeRunFileFor(t, cfg, server)
			args := append([]string{"spawn", "--name", "account worker"}, tc.args...)
			out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, args...)
			if err != nil {
				t.Fatalf("spawn=%v stderr=%s", err, errOut)
			}
			if !strings.Contains(out, `spawned session new-session "account worker" (idle)`) {
				t.Fatalf("confirmation=%s", out)
			}
			for field, want := range map[string]string{"providerAccountId": tc.wantAccount, "harness": tc.wantAgent, "mode": tc.wantMode, "model": tc.wantModel, "projectId": tc.wantProject} {
				raw, exists := received[field]
				if want == "" {
					if exists {
						t.Fatalf("default field %s was explicitly sent: %s", field, raw)
					}
					continue
				}
				var got string
				if err = json.Unmarshal(raw, &got); err != nil || got != want {
					t.Fatalf("field %s=%s want=%q err=%v", field, raw, want, err)
				}
			}
			for _, private := range []string{"ticket", "ticketHash", "authId", "credentialRef", "controlKey", "inferenceKey"} {
				if _, exists := received[private]; exists {
					t.Fatalf("CLI submitted private field %s", private)
				}
			}
			count := 0
			for _, request := range requests {
				if request == "POST /api/v1/sessions" {
					count++
				}
				if strings.Contains(request, "proxy") || strings.Contains(request, "credentials") {
					t.Fatalf("CLI bypassed daemon=%s", request)
				}
			}
			if count != 1 {
				t.Fatalf("spawn count=%d requests=%v", count, requests)
			}
		})
	}
}
func TestProviderAccountSpawnCLIDaemonFailuresKeepCodeAndRequestID(t *testing.T) {
	cases := []struct {
		code, message string
		status        int
	}{
		{"PROVIDER_LOGIN_REQUIRED", "Please sign in again", 409},
		{"PROVIDER_ACCOUNT_RECOVERY_REQUIRED", "Account routing needs recovery", 409},
		{"PROVIDER_ACCOUNT_BUSY", "Wait until affected sessions are idle", 409},
		{"PROVIDER_ACCOUNT_NOT_FOUND", "Selected account was removed", 404},
		{"PROVIDER_ACCOUNT_INCOMPATIBLE", "Choose a Codex account", 400},
		{"INTERNAL_ERROR", "Helper is not available", 500},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			cfg := setConfigEnv(t)
			var requests []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				appendPrimaryRequest(&requests, r)
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/v1/projects/demo" {
					_, _ = io.WriteString(w, `{"project":{"id":"demo","path":"/repo/demo"}}`)
					return
				}
				if r.URL.Path == "/api/v1/agents/readiness/ensure" {
					_, _ = io.WriteString(w, authorizedAgentsJSON("codex"))
					return
				}
				if r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions" {
					w.WriteHeader(tc.status)
					_ = json.NewEncoder(w).Encode(map[string]any{"code": tc.code, "message": tc.message, "requestId": "account-request-42"})
					return
				}
				http.NotFound(w, r)
			}))
			t.Cleanup(server.Close)
			writeRunFileFor(t, cfg, server)
			out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "spawn", "--project", "demo", "--agent", "codex", "--account", "removed-account", "--name", "worker")
			if err == nil {
				t.Fatal("daemon account error reported success")
			}
			var apiErr apiResponseError
			if !errors.As(err, &apiErr) {
				t.Fatalf("daemon error envelope was lost=%v", err)
			}
			if apiErr.ErrorBody.Code != tc.code || apiErr.ErrorBody.Message != tc.message || apiErr.ErrorBody.RequestID != "account-request-42" {
				t.Fatalf("error=%+v", apiErr.ErrorBody)
			}
			if strings.Contains(out, "spawned session") {
				t.Fatal("failed managed spawn printed success")
			}
			for _, request := range requests {
				if strings.Contains(request, "kill") || strings.Contains(request, "DELETE") {
					t.Fatalf("failed admission triggered an unrelated rollback=%s", request)
				}
			}
		})
	}
}
func TestProviderAccountSpawnCLILeavesLocalCredentialsUntouched(t *testing.T) {
	cfg := setConfigEnv(t)
	scratch := t.TempDir()
	codex := filepath.Join(scratch, "codex")
	claude := filepath.Join(scratch, "claude")
	if err := os.MkdirAll(codex, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(claude, 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		filepath.Join(codex, "auth.json"):          []byte(`{"fake":"native-device-auth"}`),
		filepath.Join(codex, "config.toml"):        []byte("model = \"native-model\"\n"),
		filepath.Join(claude, ".credentials.json"): []byte(`{"fake":"native-claude-auth"}`),
	}
	for path, data := range files {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("CODEX_HOME", codex)
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	var received spawnRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/projects/demo":
			_, _ = io.WriteString(w, `{"project":{"id":"demo","path":"/repo/demo"}}`)
		case "/api/v1/agents/readiness/ensure":
			_, _ = io.WriteString(w, authorizedAgentsJSON("codex"))
		case "/api/v1/sessions":
			if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
				t.Error(err)
			}
			_, _ = io.WriteString(w, `{"session":{"id":"new","status":"idle"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)
	if _, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "spawn", "--project", "demo", "--agent", "codex", "--account", "account-a", "--name", "worker"); err != nil {
		t.Fatal(err)
	}
	if received.ProviderAccountID != "account-a" {
		t.Fatal("explicit pin was not delegated to daemon")
	}
	for path, want := range files {
		got, err := os.ReadFile(path)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("global credential/configuration file changed=%s", path)
		}
	}
}
func TestProviderAccountSpawnCLIHelpAndMissingArgumentStayUsageErrors(t *testing.T) {
	out, _, err := executeCLI(t, Deps{}, "spawn", "--help")
	if err != nil || !strings.Contains(out, "--account") || !strings.Contains(out, "provider primary") {
		t.Fatalf("account help=%s err=%v", out, err)
	}
	_, _, err = executeCLI(t, Deps{}, "spawn", "--account")
	var usage usageError
	if err == nil || !errors.As(err, &usage) {
		t.Fatalf("missing account flag argument=%v", err)
	}
}
