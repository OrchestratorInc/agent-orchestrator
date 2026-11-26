package claudecode

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestClaudeAuthDiagnosticsExplainRenewalWithoutSendingToken(t *testing.T) {
	now := time.Now()
	pinClaudeNow(t, now)
	requests := 0
	server := withStubValidator(t, func(w http.ResponseWriter, _ *http.Request) { requests++; w.WriteHeader(http.StatusUnauthorized) })
	status, ok, step := (&Plugin{}).probeResolvedAuthDiagnostics(context.Background(), storedLoginContext(now.Add(-time.Minute), true, server.URL), true)
	if !ok || status != ports.AgentAuthStatusConfigured || step.Code != "token_renewal_pending" || step.Source != "keychain" || step.NextStep != "" {
		t.Fatalf("status=%s ok=%v step=%+v", status, ok, step)
	}
	if requests != 0 {
		t.Fatal("renewable token sent to provider")
	}
}

func TestClaudeAuthDiagnosticsKeepProviderEvidenceAndCacheAge(t *testing.T) {
	for _, httpStatus := range []int{200, 401, 503} {
		t.Run(http.StatusText(httpStatus), func(t *testing.T) {
			InvalidateAuthCache()
			t.Cleanup(InvalidateAuthCache)
			server := withStubValidator(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(httpStatus)
				_, _ = w.Write([]byte(`{"data":[{"id":"claude-test"}],"error":{"message":"secret-token"}}`))
			})
			resolved := storedLoginContext(time.Time{}, false, server.URL)
			resolved.credential.Source = "ANTHROPIC_API_KEY"
			status, _, step := (&Plugin{}).probeResolvedAuthDiagnostics(context.Background(), resolved, true)
			want := map[int]string{200: "provider_accepted", 401: "provider_rejected", 503: "provider_inconclusive"}[httpStatus]
			if step.Code != want || step.HTTPStatus != httpStatus || step.Source != "ANTHROPIC_API_KEY" || step.Cached {
				t.Fatalf("step=%+v", step)
			}
			if httpStatus == 503 && status != ports.AgentAuthStatusUnknown {
				t.Fatalf("status=%s", status)
			}
			payload, _ := json.Marshal(step)
			if strings.Contains(string(payload), "secret-token") || strings.Contains(string(payload), server.URL) {
				t.Fatalf("sensitive payload=%s", payload)
			}
			if httpStatus != 503 {
				_, _, cached := (&Plugin{}).probeResolvedAuthDiagnostics(context.Background(), resolved, false)
				if !cached.Cached || !cached.CheckedAt.Equal(step.CheckedAt) {
					t.Fatalf("cached=%+v first=%+v", cached, step)
				}
			}
		})
	}
}

// `claude auth status` renews an expired stored login, and a refused renewal
// wipes it, so a check racing the account's sessions signs it out about eight
// hours after sign-in (#8724). An expired but renewable login is therefore not
// put to the CLI; a current one still is.
func TestResolveProviderContextLeavesAnExpiredRenewableLoginToTheNextSession(t *testing.T) {
	now := time.Now()
	pinClaudeNow(t, now)
	for _, tc := range []struct {
		name      string
		expiresAt time.Time
		wantCLI   int
	}{
		{name: "expired, renewable", expiresAt: now.Add(-time.Hour), wantCLI: 0},
		{name: "current", expiresAt: now.Add(time.Hour), wantCLI: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, name := range []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL"} {
				t.Setenv(name, "")
			}
			dir := t.TempDir()
			t.Setenv("CLAUDE_CONFIG_DIR", dir)
			t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", dir)
			login, err := json.Marshal(map[string]any{"claudeAiOauth": map[string]any{
				"accessToken": "sk-ant-oat01-test", "refreshToken": "sk-ant-ort01-test",
				"expiresAt": tc.expiresAt.UnixMilli(), "refreshTokenExpiresAt": now.Add(30 * 24 * time.Hour).UnixMilli(),
			}})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), login, 0o600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			resolved := (&Plugin{}).resolveProviderContext(context.Background(), "claude", t.TempDir(),
				map[string]string{"CLAUDE_CONFIG_DIR": dir},
				func(context.Context, string, string, map[string]string) (claudeAuthReport, bool) {
					calls++
					return claudeAuthReport{}, false
				})
			if calls != tc.wantCLI {
				t.Fatalf("claude auth status ran %d times, want %d", calls, tc.wantCLI)
			}
			if !resolved.found || resolved.credential.ExpiredButRenewable(now) != (tc.wantCLI == 0) {
				t.Fatalf("resolved %+v found=%v, want the stored login", resolved.credential.ExpiresAt, resolved.found)
			}
		})
	}
}

// When the CLI is asked, it may renew the login while it runs; the credential
// the check goes on with is the one it left, not the one read before it.
func TestResolveProviderContextReadsTheLoginTheCLILeft(t *testing.T) {
	now := time.Now()
	pinClaudeNow(t, now)
	for _, name := range []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL"} {
		t.Setenv(name, "")
	}
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	t.Setenv("CLAUDE_SECURESTORAGE_CONFIG_DIR", dir)
	writeLogin := func(token string) {
		login, err := json.Marshal(map[string]any{"claudeAiOauth": map[string]any{
			"accessToken": token, "refreshToken": "sk-ant-ort01-test",
			"expiresAt": now.Add(time.Hour).UnixMilli(), "refreshTokenExpiresAt": now.Add(30 * 24 * time.Hour).UnixMilli(),
		}})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), login, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeLogin("sk-ant-oat01-before")
	resolved := (&Plugin{}).resolveProviderContext(context.Background(), "claude", t.TempDir(),
		map[string]string{"CLAUDE_CONFIG_DIR": dir},
		func(context.Context, string, string, map[string]string) (claudeAuthReport, bool) {
			writeLogin("sk-ant-oat01-renewed") // the CLI renews the token while it runs
			return claudeAuthReport{}, false
		})
	if !resolved.found || resolved.credential.Secret != "sk-ant-oat01-renewed" {
		t.Fatal("the check went on with the login read before the CLI ran, not the one it left")
	}
}
