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

func TestManagedAccountsCredentialInputBoundary(t *testing.T) {
	for _, tt := range []struct {
		name, action, input string
		valid               bool
	}{
		{"empty key", "add-key", "", false},
		{"whitespace key", "add-key", " \n", false},
		{"multiple keys", "add-key", "secret-a\nsecret-b", false},
		{"key exact limit", "add-key", strings.Repeat("a", 8192), true},
		{"key over limit", "add-key", strings.Repeat("a", 8193), false},
		{"escaped payload over limit", "add-key", strings.Repeat("<", 8192), false},
		{"null import", "import", "null", false},
		{"array import", "import", `[{"key":"secret-marker"}]`, false},
		{"empty object", "import", "{}", false},
		{"trailing object", "import", `{"key":"secret-marker"}{}`, false},
		{"truncated object", "import", `{"key":"secret-marker"`, false},
		{"exact import limit", "import", `{"key":"` + strings.Repeat("a", (1<<20)-10) + `"}`, true},
		{"over import limit", "import", `{"key":"` + strings.Repeat("a", (1<<20)-9) + `"}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			capture := &agentSwitchRequestCapture{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capture.record(r)
				_, _ = io.WriteString(w, `{"revision":1,"availability":"ready","accounts":[]}`)
			}))
			t.Cleanup(server.Close)
			writeRunFileFor(t, cfg, server)
			out, errOut, err := executeCLI(t, Deps{In: strings.NewReader(tt.input), ProcessAlive: func(int) bool { return true }}, "accounts", tt.action, "--provider", "codex", "--operation-id", "input-a", "--stdin", "--json")
			_, _, _, count := capture.snapshot()
			if tt.valid {
				if err != nil || count != 1 || !json.Valid([]byte(out)) {
					t.Fatalf("valid input: %v count=%d", err, count)
				}
			} else if ExitCode(err) != 2 || count != 0 {
				t.Fatalf("invalid input: %v count=%d", err, count)
			}
			if strings.Contains(out+errOut, "secret-marker") || err != nil && strings.Contains(err.Error(), "secret-marker") {
				t.Fatal("input echoed")
			}
		})
	}
}

func TestManagedAccountsLoginStatusBoundary(t *testing.T) {
	for _, state := range []string{"pending", "completed", "failed", "expired", "missing", "stale"} {
		t.Run(state, func(t *testing.T) {
			cfg := setConfigEnv(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				logins := []map[string]any{{"id": "login-a", "status": state, "authorizationUrl": "secret-marker", "userCode": "secret-marker"}}
				if state == "missing" {
					logins[0]["id"] = "login-other"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"availability": "ready", "stale": state == "stale", "oauthSessions": logins})
			}))
			t.Cleanup(server.Close)
			writeRunFileFor(t, cfg, server)
			out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "accounts", "login-status", "login-a", "--json")
			if state == "missing" || state == "stale" {
				if ExitCode(err) != 1 || out != "" {
					t.Fatal("unknown sign-in state accepted")
				}
				return
			}
			if err != nil || !strings.Contains(out, state) || strings.Contains(out, "secret-marker") {
				t.Fatalf("status lost or secret exposed: %v %s", err, out)
			}
		})
	}
}

func TestManagedAccountsRemovalResponseOwnership(t *testing.T) {
	for _, action := range []string{"remove", "removal-impact", "removal-retry", "removal-cancel"} {
		for _, field := range []string{"id", "accountId", "impact"} {
			if action == "removal-impact" && field != "accountId" {
				continue
			}
			t.Run(action+"/"+field, func(t *testing.T) {
				cfg := setConfigEnv(t)
				capture := &agentSwitchRequestCapture{}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasPrefix(r.URL.Path, "/internal/") {
						w.WriteHeader(http.StatusNoContent)
						return
					}
					capture.record(r)
					response := map[string]any{"id": "remove-a", "accountId": "amc_a", "phase": "requested", "impact": map[string]any{"accountId": "amc_a", "revision": 0, "sessions": []any{}}}
					if r.Method == http.MethodPost || action == "removal-impact" {
						if field == "impact" {
							response["impact"] = map[string]any{"accountId": "amc_other"}
						} else {
							response[field] = "foreign"
						}
					}
					_ = json.NewEncoder(w).Encode(response)
				}))
				t.Cleanup(server.Close)
				writeRunFileFor(t, cfg, server)
				args := []string{"accounts", action, "amc_a", "--json"}
				if action == "remove" {
					args = append(args, "--operation-id", "remove-a", "--expected-revision", "0", "--confirm")
				} else if action != "removal-impact" {
					args = append(args, "remove-a")
				}
				out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, args...)
				if ExitCode(err) != 1 || out != "" {
					t.Fatalf("foreign response exposed: %v %s", err, out)
				}
			})
		}
	}
}

func TestManagedAccountsRemovalHumanStates(t *testing.T) {
	for _, phase := range []string{"requested", "stopping", "revoked", "complete", "recovery_required", "cancelled"} {
		t.Run(phase, func(t *testing.T) {
			cfg := setConfigEnv(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "remove-a", "accountId": "amc_a", "phase": phase, "canCancel": phase == "requested", "recoveryRequired": phase == "recovery_required", "impact": map[string]any{"accountId": "amc_a", "revision": 3, "sessions": []map[string]any{{"sessionId": "session-a", "bindingRevision": 7, "stopped": false, "runtimeHandle": "secret-marker"}, {"sessionId": "dormant-b", "bindingRevision": 2, "stopped": true}}}})
			}))
			t.Cleanup(server.Close)
			writeRunFileFor(t, cfg, server)
			out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "accounts", "removal-status", "amc_a", "remove-a")
			if err != nil || !strings.Contains(out, "phase: "+phase) || !strings.Contains(out, "bound sessions: 2") || !strings.Contains(out, "dormant-b") || strings.Contains(out, "secret-marker") {
				t.Fatalf("state projection: %v %s", err, out)
			}
		})
	}
}

type managedCredentialReadError struct{}

func (managedCredentialReadError) Read([]byte) (int, error) { return 0, errors.New("secret-marker") }

func TestManagedAccountsTransportAndInputErrors(t *testing.T) {
	for _, inputFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "response", true: "input"}[inputFailure], func(t *testing.T) {
			cfg := setConfigEnv(t)
			capture := &agentSwitchRequestCapture{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capture.record(r)
				_, _ = io.WriteString(w, `{"revision":"secret-marker"}`)
			}))
			t.Cleanup(server.Close)
			writeRunFileFor(t, cfg, server)
			var in io.Reader = strings.NewReader("secret-marker")
			if inputFailure {
				in = managedCredentialReadError{}
			}
			out, errOut, err := executeCLI(t, Deps{In: in, ProcessAlive: func(int) bool { return true }}, "accounts", "add-key", "--provider", "codex", "--operation-id", "add-a", "--stdin")
			if ExitCode(err) != 1 || out != "" || strings.Contains(err.Error()+errOut, "secret-marker") {
				t.Fatalf("unsafe error: %v", err)
			}
			_, _, _, count := capture.snapshot()
			want := 1
			if inputFailure {
				want = 0
			}
			if count != want {
				t.Fatalf("requests=%d", count)
			}
		})
	}
}
