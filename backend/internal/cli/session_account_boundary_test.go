package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSessionAccountModesTimingAndOutput(t *testing.T) {
	for _, native := range []bool{false, true} {
		for _, policy := range []string{"drain", "interrupt"} {
			for _, asJSON := range []bool{false, true} {
				t.Run(map[bool]string{false: "managed", true: "native"}[native]+"/"+policy+map[bool]string{false: "/human", true: "/json"}[asJSON], func(t *testing.T) {
					cfg := setConfigEnv(t)
					capture := &agentSwitchRequestCapture{}
					mode, target := "managed", "amc_b"
					if native {
						mode, target = "native", ""
					}
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if strings.HasPrefix(r.URL.Path, "/internal/") {
							w.WriteHeader(http.StatusNoContent)
							return
						}
						capture.record(r)
						_ = json.NewEncoder(w).Encode(map[string]any{"id": "switch-a", "sessionId": "session-a", "sourceMode": "managed", "sourceAccountId": "amc_a", "targetMode": mode, "targetAccountId": target, "policy": policy, "newConversation": true, "phase": "waiting", "privateEndpoint": "secret-marker"})
					}))
					t.Cleanup(server.Close)
					writeRunFileFor(t, cfg, server)
					args := []string{"session", "account", "switch", "session-a", "--expected-revision", "7", "--policy", policy, "--operation-id", "switch-a", "--new-conversation"}
					if native {
						args = append(args, "--native")
					} else {
						args = append(args, "--account", target)
					}
					if asJSON {
						args = append(args, "--json")
					}
					out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, args...)
					if err != nil || ExitCode(err) != 0 || strings.Contains(out+errOut, "secret-marker") {
						t.Fatalf("switch output: %v", err)
					}
					method, path, body, count := capture.snapshot()
					var input map[string]any
					if json.Unmarshal(body, &input) != nil || input["mode"] != mode || input["policy"] != policy || input["newConversation"] != true || count != 1 || method != "POST" || path != "/api/v1/sessions/session-a/account-switches" {
						t.Fatal("explicit choice changed")
					}
					if native {
						if _, ok := input["accountId"]; ok {
							t.Fatal("native mode invented an account")
						}
					} else if input["accountId"] != target {
						t.Fatal("managed target changed")
					}
					if asJSON {
						if !json.Valid([]byte(out)) {
							t.Fatal("invalid JSON")
						}
					} else if !strings.Contains(out, "target mode: "+mode) || !strings.Contains(out, "policy: "+policy) || !strings.Contains(out, "phase: waiting") {
						t.Fatalf("human output hides choice: %s", out)
					}
				})
			}
		}
	}
}

func TestSessionAccountReadAndResponseOwnership(t *testing.T) {
	for _, action := range []string{"get", "switch", "status", "retry", "cancel"} {
		for _, foreign := range []string{"session", "operation", "none"} {
			t.Run(action+"/"+foreign, func(t *testing.T) {
				cfg := setConfigEnv(t)
				capture := &agentSwitchRequestCapture{}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasPrefix(r.URL.Path, "/internal/") {
						w.WriteHeader(http.StatusNoContent)
						return
					}
					capture.record(r)
					op := map[string]any{"id": "switch-a", "sessionId": "session-a", "phase": "waiting", "targetAccountId": "amc_b", "runtimeHandle": "secret-marker"}
					if foreign == "session" {
						op["sessionId"] = "session-other"
					}
					if foreign == "operation" {
						op["id"] = "switch-other"
					}
					if action == "get" {
						binding := map[string]any{"sessionId": "session-a", "accountId": "amc_a", "mode": "managed", "revision": 7, "switch": op}
						if foreign == "operation" {
							binding["sessionId"] = "session-other"
						}
						_ = json.NewEncoder(w).Encode(binding)
					} else {
						_ = json.NewEncoder(w).Encode(op)
					}
				}))
				t.Cleanup(server.Close)
				writeRunFileFor(t, cfg, server)
				args := []string{"session", "account", action, "session-a"}
				switch action {
				case "get":
				case "switch":
					args = append(args, "--account", "amc_b", "--policy", "drain", "--expected-revision", "7", "--operation-id", "switch-a")
				default:
					args = append(args, "switch-a")
				}
				out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, append(args, "--json")...)
				if foreign != "none" {
					if ExitCode(err) != 1 || out != "" {
						t.Fatalf("foreign response accepted: %v %s", err, out)
					}
					return
				}
				if err != nil || !json.Valid([]byte(out)) || strings.Contains(out, "secret-marker") {
					t.Fatalf("valid response: %v %s", err, out)
				}
				if action == "get" {
					var response map[string]any
					_ = json.Unmarshal([]byte(out), &response)
					if response["accountId"] != "amc_a" {
						t.Fatal("pending target replaced committed account")
					}
				}
			})
		}
	}
}

func TestSessionAccountDaemonErrorsAndHelp(t *testing.T) {
	for _, status := range []int{400, 404, 409, 500, 501, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			cfg := setConfigEnv(t)
			capture := &agentSwitchRequestCapture{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capture.record(r)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"code":"ACCOUNTS_MANAGER_CONTROL_CONFLICT","message":"secret-marker http://127.0.0.1/private","requestId":"host/random-00042"}`)
			}))
			t.Cleanup(server.Close)
			writeRunFileFor(t, cfg, server)
			out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "account", "retry", "session-a", "switch-a")
			if ExitCode(err) != 1 || !strings.Contains(err.Error(), "host/random-00042") || !strings.Contains(err.Error(), "ACCOUNTS_MANAGER_CONTROL_CONFLICT") || out != "" || strings.Contains(errOut+err.Error(), "secret-marker") {
				t.Fatalf("unsafe error: %v", err)
			}
			_, _, _, count := capture.snapshot()
			if count != 1 {
				t.Fatalf("unexpected fallback: %d requests", count)
			}
		})
	}
	for _, args := range [][]string{{"session", "account", "--help"}, {"session", "account", "switch", "--help"}, {"accounts", "--help"}, {"accounts", "remove", "--help"}} {
		out, _, err := executeCLI(t, Deps{}, args...)
		if err != nil || !strings.Contains(out, "Usage:") {
			t.Fatalf("help missing: %v %s", err, out)
		}
	}
	for _, args := range [][]string{{"session", "account", "get"}, {"session", "account", "retry", "session-a"}, {"session", "account", "get", "session-a", "extra"}, {"accounts", "ls", "extra"}, {"accounts", "remove"}} {
		_, _, err := executeCLI(t, Deps{}, args...)
		if ExitCode(err) != 2 {
			t.Fatalf("argument misuse exit=%d: %v", ExitCode(err), err)
		}
	}
}

func TestSessionAccountRecoveryIsExplicitAndRepeatable(t *testing.T) {
	cfg := setConfigEnv(t)
	capture := &agentSwitchRequestCapture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/internal/") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		capture.record(r)
		_, _ = io.WriteString(w, `{"id":"switch-a","sessionId":"session-a","phase":"recovery_required","recoveryRequired":true}`)
	}))
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)
	for i := 0; i < 2; i++ {
		out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "account", "retry", "session-a", "switch-a", "--json")
		if err != nil || !strings.Contains(out, "recovery_required") {
			t.Fatalf("retry %d: %v", i, err)
		}
	}
	method, path, body, count := capture.snapshot()
	if count != 2 || method != "POST" || path != "/api/v1/sessions/session-a/account-switches/switch-a/retry" || strings.TrimSpace(string(body)) != "{}" {
		t.Fatal("retry silently replaced operation or account")
	}
}
