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

func TestSpawnInitialAccountCLI(t *testing.T) {
	for _, mode := range []string{"native", "managed"} {
		t.Run(mode, func(t *testing.T) {
			cfg := setConfigEnv(t)
			var choice map[string]string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/v1/sessions/account-selection":
					_, _ = io.WriteString(w, `{"initialSelection":true}`)
				case "/api/v1/agents/readiness/ensure":
					_, _ = io.WriteString(w, readinessAgentsJSON("codex", "installed", "unauthorized"))
				case "/api/v1/sessions":
					var input struct {
						Account map[string]string `json:"account"`
					}
					if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
						t.Error(err)
					}
					choice = input.Account
					_, _ = io.WriteString(w, `{"session":{"id":"scratch-1","status":"idle"}}`)
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(srv.Close)
			writeRunFileFor(t, cfg, srv)
			args := []string{"spawn", "--standalone", "--agent", "codex", "--name", "account test", "--account-mode", mode}
			if mode == "managed" {
				args = append(args, "--account-id", "account-a")
			}
			_, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, args...)
			if err != nil || choice["mode"] != mode || (mode == "managed" && choice["accountId"] != "account-a") {
				t.Fatalf("CLI did not forward explicit choice: %v, stderr=%s", err, errOut)
			}
			if mode == "managed" && strings.Contains(errOut, "need auth") {
				t.Fatal("managed selection displayed unrelated native login warning")
			}
		})
	}
}

func TestSpawnInitialAccountCLIUsage(t *testing.T) {
	for _, flags := range [][]string{
		{"--account-id", "account-a"}, {"--account-mode", "managed"},
		{"--account-mode", "native", "--account-id", "account-a"}, {"--account-mode", "fallback"},
		{"--account-mode", ""}, {"--account-mode", "managed", "--account-id", " "},
	} {
		args := append([]string{"spawn", "--standalone", "--agent", "codex", "--name", "account test"}, flags...)
		_, _, err := executeCLI(t, Deps{}, args...)
		var usage usageError
		if !errors.As(err, &usage) {
			t.Fatalf("invalid choice should be a usage error: %v", err)
		}
	}
}

func TestSpawnInitialAccountCLICapabilityAndErrors(t *testing.T) {
	for _, scenario := range []string{"unsupported", "missing capability", "daemon error"} {
		t.Run(scenario, func(t *testing.T) {
			cfg := setConfigEnv(t)
			spawns := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/v1/sessions/account-selection" {
					switch scenario {
					case "unsupported":
						_, _ = io.WriteString(w, `{"initialSelection":false}`)
					case "missing capability":
						_, _ = io.WriteString(w, `{}`)
					default:
						_, _ = io.WriteString(w, `{"initialSelection":true}`)
					}
					return
				}
				if r.URL.Path == "/api/v1/sessions" {
					spawns++
					w.WriteHeader(http.StatusConflict)
					_, _ = io.WriteString(w, `{"error":"conflict","code":"ACCOUNT_DELETING","message":"selected account is being removed","requestId":"initial-cli-request"}`)
					return
				}
				http.NotFound(w, r)
			}))
			t.Cleanup(srv.Close)
			writeRunFileFor(t, cfg, srv)
			_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "spawn", "--standalone", "--agent", "codex", "--name", "test", "--account-mode", "managed", "--account-id", "account-a", "--skip-agent-check")
			var usage usageError
			if err == nil || errors.As(err, &usage) {
				t.Fatalf("daemon failure should be a runtime error: %v", err)
			}
			if scenario == "daemon error" {
				if spawns != 1 || !strings.Contains(err.Error(), "ACCOUNT_DELETING") || !strings.Contains(err.Error(), "initial-cli-request") {
					t.Fatalf("daemon error lost identity: %v", err)
				}
			} else if spawns != 0 {
				t.Fatal("explicit choice sent to an unsupported daemon")
			}
		})
	}
}
