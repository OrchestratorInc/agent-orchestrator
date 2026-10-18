package claudecode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/pkg/agentcreds"
)

// roundTripFunc adapts a function to an http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// TestProviderModelsProbeBudgetCoversGatewayListing pins the 2026-10-14 model
// picker failure: the whole discovery probe — credential resolution plus the
// gateway's model listing — ran inside agentcreds.DefaultTimeout, a budget
// calibrated for a single api.anthropic.com probe (~350 ms round-trip), and a
// real gateway listing died with "context deadline exceeded" rendered raw
// into the picker. The probe must carry a budget strictly larger than the
// single-probe validation timeout.
func TestProviderModelsProbeBudgetCoversGatewayListing(t *testing.T) {
	clearClaudeCredentialEnv(t)
	InvalidateAuthCache()
	t.Cleanup(InvalidateAuthCache)
	t.Setenv("ANTHROPIC_API_KEY", "gateway-key")

	var remaining time.Duration
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"glm-5.3"}]}`))
	}))
	t.Cleanup(server.Close)

	previousValidator := claudeValidator
	claudeValidator = func() *agentcreds.Validator {
		return agentcreds.New(&http.Client{
			Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if deadline, ok := r.Context().Deadline(); ok {
					remaining = time.Until(deadline)
				}
				r.RequestURI = r.URL.RequestURI()
				return http.DefaultTransport.RoundTrip(r)
			}),
		})
	}
	t.Cleanup(func() { claudeValidator = previousValidator })

	previous := claudeModelAuthReport
	claudeModelAuthReport = func(context.Context, string, string, map[string]string) (claudeAuthReport, bool) {
		loggedIn := false
		return claudeAuthReport{LoggedIn: &loggedIn, APIProvider: "firstParty"}, true
	}
	t.Cleanup(func() { claudeModelAuthReport = previous })

	project := t.TempDir()
	writeClaudeAuthSettings(t, filepath.Join(project, ".claude", "settings.local.json"), map[string]string{
		"ANTHROPIC_BASE_URL": server.URL, "ANTHROPIC_API_KEY": "gateway-key",
	})

	models, err := ProviderModels(context.Background(), "/fixture/claude", project, map[string]string{"ANTHROPIC_API_KEY": "gateway-key"})
	if err != nil {
		t.Fatalf("provider models through a gateway: %v", err)
	}
	if len(models) == 0 || models[0].ID != "glm-5.3" {
		t.Fatalf("models = %v, want the gateway listing", models)
	}
	if remaining == 0 || remaining <= agentcreds.DefaultTimeout {
		t.Fatalf("probe budget remaining = %s, want strictly more than the single-probe validation timeout %s", remaining, agentcreds.DefaultTimeout)
	}
}
