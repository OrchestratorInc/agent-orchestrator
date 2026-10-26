package claudecode

import (
	"context"
	"encoding/json"
	"net/http"
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
