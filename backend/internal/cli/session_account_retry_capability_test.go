package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSessionAccountRetryCapability(t *testing.T) {
	for _, available := range []bool{false, true} {
		for _, asJSON := range []bool{false, true} {
			t.Run(fmt.Sprintf("retry=%t/json=%t", available, asJSON), func(t *testing.T) {
				cfg := setConfigEnv(t)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method+" "+r.URL.Path == cliInvokedRequest {
						w.WriteHeader(http.StatusNoContent)
						return
					}
					if r.Method == http.MethodGet && r.URL.Path == "/healthz" {
						w.WriteHeader(http.StatusOK)
						return
					}
					if r.Method != http.MethodGet || r.URL.Path != "/api/v1/sessions/session-a/account-switches/switch-a" {
						t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprintf(w, `{"id":"switch-a","sessionId":"session-a","phase":"waiting","canRetry":%t}`, available)
				}))
				t.Cleanup(server.Close)
				writeRunFileFor(t, cfg, server)
				args := []string{"session", "account", "status", "session-a", "switch-a"}
				if asJSON {
					args = append(args, "--json")
				}
				out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, args...)
				if err != nil {
					t.Fatal(err)
				}
				if asJSON {
					var response map[string]any
					if json.Unmarshal([]byte(out), &response) != nil || response["canRetry"] != available {
						t.Fatalf("retry capability lost: %s", out)
					}
				} else if !strings.Contains(out, fmt.Sprintf("retry available: %t", available)) {
					t.Fatalf("retry capability lost: %s", out)
				}
			})
		}
	}
}
