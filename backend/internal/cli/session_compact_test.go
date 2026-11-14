package cli

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func compactCommandServer(t *testing.T, status int, body string) (*httptest.Server, *int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/sessions/demo-1/conversation/compact" {
			http.NotFound(w, r)
			return
		}
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestSessionCompact(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"reports the context being reclaimed", `{"tokensBefore":214000}`, "compacting demo-1 (214000 tokens of context); the result will appear on its timeline"},
		{"reports a synchronous result", `{"tokensBefore":214000,"tokensAfter":31000}`, "compacted demo-1 from 214000 to 31000 tokens"},
		{"claims no figure the provider did not give", `{}`, "compacting demo-1; the result will appear on its timeline"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			srv, calls := compactCommandServer(t, http.StatusAccepted, tc.body)
			writeRunFileFor(t, cfg, srv)
			out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "compact", "demo-1")
			if err != nil {
				t.Fatalf("session compact failed: %v\nstderr=%s", err, errOut)
			}
			if strings.TrimSpace(out) != tc.want || *calls != 1 {
				t.Fatalf("output = %q, calls = %d", out, *calls)
			}
		})
	}
}

func TestSessionCompactBusyIsRuntimeError(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := compactCommandServer(t, http.StatusConflict,
		`{"error":"conflict","code":"CHAT_COMPACTION_BUSY","message":"finish or stop the current turn before compacting"}`)
	writeRunFileFor(t, cfg, srv)
	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "compact", "demo-1")
	if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), "CHAT_COMPACTION_BUSY") {
		t.Fatalf("err = %v exit = %d", err, ExitCode(err))
	}
}

func TestSessionCompactNeedsAnID(t *testing.T) {
	setConfigEnv(t)
	_, _, err := executeCLI(t, Deps{}, "session", "compact")
	if ExitCode(err) != 2 {
		t.Fatalf("exit = %d, want 2 (usage)", ExitCode(err))
	}
}
