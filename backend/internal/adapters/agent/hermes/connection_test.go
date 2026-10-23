package hermes

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestHermesVerifiesSelectedConnection(t *testing.T) {
	for _, result := range []ports.AgentAuthStatus{ports.AgentAuthStatusAuthorized, ports.AgentAuthStatusUnauthorized, ports.AgentAuthStatusConfigured} {
		t.Run(string(result), func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HERMES_HOME", home)
			t.Setenv("HERMES_MANAGED_DIR", filepath.Join(home, "missing"))
			t.Setenv("GLM_API_KEY", "stale-inherited-key")
			for name, content := range map[string]string{
				"config.yaml": "model:\n  provider: zai\n  default: glm-5.2\n  base_url: https://api.z.ai/api/coding/paas/v4\n",
				".env":        "GLM_API_KEY=selected-key\n",
				"auth.json":   `{"credential_pool":{"zai":[{"source":"env:GLM_API_KEY","base_url":"https://api.z.ai/api/coding/paas/v4"}]}}`,
			} {
				if err := os.WriteFile(filepath.Join(home, name), []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			old := verifyConnection
			t.Cleanup(func() { verifyConnection = old })
			calls := 0
			verifyConnection = func(_ context.Context, route authprobe.Connection, _ *http.Client) (ports.AgentAuthStatus, domain.AgentAuthDiagnostic) {
				calls++
				if route.APIKey != "selected-key" || route.Model != "glm-5.2" || route.BaseURL != "https://api.z.ai/api/coding/paas/v4" {
					t.Fatal("wrong selected credential/model/endpoint")
				}
				return result, domain.NewAgentAuthDiagnostic("provider_validation", "provider_accepted")
			}
			status, identity, steps, err := verifyAuthentication(t.Context(), ports.AgentAuthStatusConfigured, domain.AgentAccountIdentity{}, nil)
			if status != result || calls != 1 || err != nil || len(steps) != 1 || identity.Provider != "zai" {
				t.Fatalf("status=%s calls=%d err=%v identity=%+v steps=%v", status, calls, err, identity, steps)
			}
		})
	}
}

func TestHermesLeavesAmbiguousRoutesUnverified(t *testing.T) {
	for _, tc := range []struct{ name, config, pool string }{
		{"unsupported provider", "model:\n  provider: anthropic\n  default: claude\n  base_url: https://api.anthropic.com\n", `{}`},
		{"auto endpoint would guess billing route", "model:\n  provider: zai\n  default: glm-5.2\n", `{}`},
		{"pool has independent credential", "model:\n  provider: zai\n  default: glm-5.2\n  base_url: https://api.z.ai/api/coding/paas/v4\n", `{"credential_pool":{"zai":[{"access_token":"other-key"}]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HERMES_MANAGED_DIR", filepath.Join(home, "missing"))
			t.Setenv("GLM_BASE_URL", "")
			for name, content := range map[string]string{"config.yaml": tc.config, "auth.json": tc.pool, ".env": "GLM_API_KEY=present-key\n"} {
				if err := os.WriteFile(filepath.Join(home, name), []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, ok := selectedConnection(home); ok {
				t.Fatal("ambiguous route considered verified")
			}
		})
	}
}
