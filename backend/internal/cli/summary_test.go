package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestSummaryUsageErrorsExitWithTwo(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "worker-1")
	tooLong := strings.Repeat("x", domain.MaxCardSummaryRunes+1)
	for name, args := range map[string][]string{
		"no arguments": {"summary"},
		"blank text":   {"summary", "   "},
		"over the cap": {"summary", tooLong},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := executeCLI(t, Deps{}, args...)
			if err == nil || ExitCode(err) != 2 {
				t.Fatalf("err=%v exit=%d, want a usage error with exit 2", err, ExitCode(err))
			}
		})
	}

	t.Setenv("AO_SESSION_ID", "")
	if _, _, err := executeCLI(t, Deps{}, "summary", "Fixing a bug"); err == nil || ExitCode(err) != 2 {
		t.Fatalf("missing session id: err=%v exit=%d", err, ExitCode(err))
	}
}

func TestSummarySendsNormalizedText(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "worker-1")
	cfg := setConfigEnv(t)
	var got summaryAPIRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/summary" {
			_ = json.NewDecoder(r.Body).Decode(&got)
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()
	writeRunFileFor(t, cfg, srv)

	multibyte := strings.Repeat("修", domain.MaxCardSummaryRunes)
	if _, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "summary", "Fixing", "the\nlogin   redirect"); err != nil {
		t.Fatal(err)
	}
	if got.SessionID != "worker-1" || got.Summary != "Fixing the login redirect" {
		t.Fatalf("request = %+v", got)
	}
	if _, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "summary", multibyte); err != nil || got.Summary != multibyte {
		t.Fatalf("summary at the character limit: err=%v sent=%d runes", err, len([]rune(got.Summary)))
	}
}
