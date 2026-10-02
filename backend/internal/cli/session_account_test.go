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

func TestSessionAccountRequiresExplicitChoiceBeforeHTTP(t *testing.T) {
	cfg := setConfigEnv(t)
	capture := &agentSwitchRequestCapture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.record(r)
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)
	base := []string{"session", "account", "switch", "session-a", "--account", "amc_b", "--expected-revision", "7", "--policy", "interrupt", "--operation-id", "switch-a"}
	for name, args := range map[string][]string{
		"missing target":    append(append([]string{}, base[:4]...), base[6:]...),
		"missing revision":  append(append([]string{}, base[:6]...), base[8:]...),
		"missing timing":    append(append([]string{}, base[:8]...), base[10:]...),
		"missing operation": append([]string{}, base[:10]...),
		"ambiguous mode":    append(append([]string{}, base...), "--native"),
		"invalid target":    {"session", "account", "switch", "session-a", "--account", "../amc_a", "--expected-revision", "7", "--policy", "interrupt", "--operation-id", "switch-a"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, args...)
			var usage usageError
			if !errors.As(err, &usage) {
				t.Fatalf("expected usage error, got %v", err)
			}
		})
	}
	_, _, _, count := capture.snapshot()
	if count != 0 {
		t.Fatalf("invalid choice sent %d requests", count)
	}
}

func TestSessionAccountRecoveryAndUnavailableEnvelope(t *testing.T) {
	for _, action := range []string{"status", "retry", "cancel"} {
		t.Run(action, func(t *testing.T) {
			cfg := setConfigEnv(t)
			capture := &agentSwitchRequestCapture{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capture.record(r)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotImplemented)
				_, _ = io.WriteString(w, `{"code":"NOT_IMPLEMENTED","message":"Account control is unavailable","requestId":"request-control-7"}`)
			}))
			t.Cleanup(server.Close)
			writeRunFileFor(t, cfg, server)
			out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "account", action, "session-a", "switch-a")
			if err == nil || !strings.Contains(err.Error(), "NOT_IMPLEMENTED") || !strings.Contains(err.Error(), "request-control-7") || out != "" {
				t.Fatalf("lost correlated unavailable state: err=%v output=%s", err, out)
			}
			method, path, body, count := capture.snapshot()
			wantMethod, wantPath := http.MethodPost, "/api/v1/sessions/session-a/account-switches/switch-a/"+action
			if action == "status" {
				wantMethod, wantPath = http.MethodGet, "/api/v1/sessions/session-a/account-switches/switch-a"
			} else if strings.TrimSpace(string(body)) != "{}" {
				t.Fatalf("recovery must not select another target: %s", body)
			}
			if count != 1 || method != wantMethod || path != wantPath {
				t.Fatalf("recovery request=%s %s count=%d", method, path, count)
			}
		})
	}
}

func TestSessionAccountSwitchPublicBoundary(t *testing.T) {
	cfg := setConfigEnv(t)
	capture := &agentSwitchRequestCapture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.record(r)
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/sessions/session-a/account-switches" {
			http.Error(w, "unexpected account selection or native operation", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"switch-a","sessionId":"session-a","sourceAccountId":"amc_a","targetAccountId":"amc_b","phase":"waiting","privateHandle":"secret-token"}`)
	}))
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)
	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }},
		"session", "account", "switch", "session-a", "--account", "amc_b", "--expected-revision", "7", "--policy", "interrupt", "--operation-id", "switch-a", "--json")
	if err != nil {
		t.Fatalf("managed account command unavailable: %v; stderr=%s", err, errOut)
	}
	method, path, body, count := capture.snapshot()
	if count != 1 || method != http.MethodPost || path != "/api/v1/sessions/session-a/account-switches" {
		t.Fatalf("managed boundary count=%d method=%s path=%s", count, method, path)
	}
	var input map[string]any
	if json.Unmarshal(body, &input) != nil || input["accountId"] != "amc_b" || input["expectedRevision"] != float64(7) || input["policy"] != "interrupt" || input["operationId"] != "switch-a" || input["mode"] != "managed" {
		t.Fatalf("explicit choice changed: %s", body)
	}
	var result map[string]any
	if json.Unmarshal([]byte(out), &result) != nil || result["phase"] != "waiting" || strings.Contains(out, "secret-token") {
		t.Fatalf("command lost pending state or exposed private data: %s", out)
	}
}
