package hermes

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestHermesInterruptedVerificationPreservesConfiguredCredentials(t *testing.T) {
	for _, provider := range []string{"zai", "unsupported"} {
		for _, code := range []string{"probe_timeout", "probe_cancelled"} {
			t.Run(provider+"/"+code, func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("HERMES_HOME", home)
				t.Setenv("HERMES_MANAGED_DIR", filepath.Join(home, "missing"))
				for name, content := range map[string]string{
					"config.yaml": "model:\n  provider: " + provider + "\n  default: glm-5.3\n  base_url: https://api.z.ai/api/coding/paas/v4\n",
					".env":        "GLM_API_KEY=selected-key\n",
				} {
					if err := os.WriteFile(filepath.Join(home, name), []byte(content), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if code == "probe_timeout" {
					var deadlineCancel context.CancelFunc
					ctx, deadlineCancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
					defer deadlineCancel()
				} else {
					cancel()
				}
				old := verifyConnection
				t.Cleanup(func() { verifyConnection = old })
				calls := 0
				verifyConnection = func(ctx context.Context, _ authprobe.Connection, _ *http.Client) (ports.AgentAuthStatus, domain.AgentAuthDiagnostic) {
					calls++
					if provider != "zai" || ctx.Err() == nil {
						t.Fatal("expected interrupted verification of the supported route")
					}
					return ports.AgentAuthStatusConfigured, domain.NewAgentAuthDiagnostic("provider_validation", code)
				}
				identity := domain.AgentAccountIdentity{Provider: provider}
				initial := domain.NewAgentAuthDiagnostic("credential_detection", "credentials_present")
				status, gotIdentity, steps, err := verifyAuthentication(ctx, ports.AgentAuthStatusConfigured, identity, []domain.AgentAuthDiagnostic{initial})
				wantCode, wantCalls := "provider_unsupported", 0
				if provider == "zai" {
					wantCode, wantCalls = code, 1
				}
				if err != nil || status != ports.AgentAuthStatusConfigured || gotIdentity.Provider != provider || calls != wantCalls {
					t.Fatalf("status=%s identity=%+v calls=%d err=%v", status, gotIdentity, calls, err)
				}
				if len(steps) != 2 || steps[0] != initial || steps[1].Code != wantCode {
					t.Fatalf("diagnostics=%+v, want retained detection and %s", steps, wantCode)
				}
			})
		}
	}
}

func TestHermesResolvesModelAliasAndDefaultPrecedence(t *testing.T) {
	for _, tc := range []struct{ name, fields, want string }{
		{"default", "  default: glm-5.2\n", "glm-5.2"},
		{"model alias", "  model: glm-5.3\n", "glm-5.3"},
		{"default wins", "  default: glm-5.2\n  model: glm-5.3\n", "glm-5.2"},
		{"empty default uses alias", "  default: ''\n  model: glm-5.3\n", "glm-5.3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HERMES_MANAGED_DIR", filepath.Join(home, "missing"))
			for name, content := range map[string]string{
				"config.yaml": "model:\n  provider: zai\n" + tc.fields + "  base_url: https://api.z.ai/api/coding/paas/v4\n",
				".env":        "GLM_API_KEY=test-key\n",
			} {
				if err := os.WriteFile(filepath.Join(home, name), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			route, _, ok := selectedConnection(home)
			if !ok || route.Model != tc.want {
				t.Fatalf("resolved=%v model=%q, want %q", ok, route.Model, tc.want)
			}
		})
	}
}
