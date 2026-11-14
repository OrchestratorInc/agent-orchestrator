package mimocode

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestAuthStatusVerifiesSelectedMiMoConnection(t *testing.T) {
	for _, tc := range []struct {
		name, model, options, wantKey string
		recent                        bool
		result                        ports.AgentAuthStatus
	}{
		{"configured model", "zai-coding-plan/glm-5.2", `{}`, "stored-key", false, ports.AgentAuthStatusAuthorized},
		{"recent model", "", `{}`, "stored-key", true, ports.AgentAuthStatusAuthorized},
		{"config key wins", "zai-coding-plan/glm-5.2", `{"apiKey":"config-key","baseURL":"https://selected.example/v1"}`, "config-key", false, ports.AgentAuthStatusAuthorized},
		{"rejected key", "zai-coding-plan/glm-5.2", `{}`, "stored-key", false, ports.AgentAuthStatusUnauthorized},
		{"offline", "zai-coding-plan/glm-5.2", `{}`, "stored-key", false, ports.AgentAuthStatusConfigured},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"zai-coding-plan":{"type":"api","key":"stored-key"}}`), 0600); err != nil {
				t.Fatal(err)
			}
			if tc.recent {
				if err := os.WriteFile(filepath.Join(dir, "model.json"), []byte(`{"recent":[{"providerID":"zai-coding-plan","modelID":"glm-5.2"}]}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("ZHIPU_API_KEY", "inherited-key")
			oldRun, oldVerify := runAuthProbe, verifyConnection
			t.Cleanup(func() { runAuthProbe, verifyConnection = oldRun, oldVerify })
			runAuthProbe = func(_ context.Context, _ string, args ...string) ([]byte, error) {
				switch strings.Join(args, " ") {
				case "auth list":
					return []byte("1 credential"), nil
				case "debug config":
					model, _ := json.Marshal(tc.model)
					return []byte(`{"model":` + string(model) + `,"provider":{"zai-coding-plan":{"options":` + tc.options + `}}}`), nil
				case "debug paths":
					return []byte("data " + dir + "\nstate " + dir), nil
				case "models":
					return []byte("zai-coding-plan/glm-5.2\nmimo/mimo-auto"), nil
				default:
					t.Fatalf("unexpected command: %v", args)
					return nil, nil
				}
			}
			calls := 0
			verifyConnection = func(_ context.Context, route authprobe.Connection, _ *http.Client) (ports.AgentAuthStatus, domain.AgentAuthDiagnostic) {
				calls++
				if route.APIKey != tc.wantKey || route.Model != "glm-5.2" || route.Provider != "zai-coding-plan" {
					t.Fatal("verified the wrong credential or model")
				}
				wantURL := "https://api.z.ai/api/coding/paas/v4"
				if tc.wantKey == "config-key" {
					wantURL = "https://selected.example/v1"
				}
				if route.BaseURL != wantURL {
					t.Fatalf("base URL=%s", route.BaseURL)
				}
				return tc.result, domain.NewAgentAuthDiagnostic("provider_validation", "provider_accepted")
			}
			ctx, evidence := ports.WithAuthEvidence(t.Context())
			status, err := (&Plugin{resolvedBinary: "mimo"}).AuthStatus(ctx)
			if err != nil || status != tc.result || calls != 1 {
				t.Fatalf("status=%s err=%v calls=%d", status, err, calls)
			}
			if steps := evidence.Diagnostics(); len(steps) != 1 || steps[0].Code != "provider_accepted" {
				t.Fatalf("diagnostics=%+v, want provider_accepted", steps)
			}
		})
	}
}

func TestMiMoDoesNotVerifyAmbiguousOrUnsupportedConfig(t *testing.T) {
	for _, tc := range []struct{ name, cfg, failedCommand, wantCode string }{
		{"unresolved model", `{"model":"other/model"}`, "", "model_selection_unresolved"},
		{"plugin", `{"plugin":["external-plugin"]}`, "", "provider_unsupported"},
		{"malformed config", `{broken`, "", "provider_inconclusive"},
		{"no model or state", `{}`, "", "provider_inconclusive"},
		{"config command failure", `{}`, "debug config", "provider_inconclusive"},
		{"paths command failure", `{"model":"zai/model"}`, "debug paths", "provider_inconclusive"},
		{"models command failure", `{"model":"zai/model"}`, "models", "provider_inconclusive"},
		{"selected key missing", `{"model":"zai/model"}`, "", "provider_inconclusive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ZHIPU_API_KEY", "")
			oldRun, oldVerify := runAuthProbe, verifyConnection
			t.Cleanup(func() { runAuthProbe, verifyConnection = oldRun, oldVerify })
			runAuthProbe = func(_ context.Context, _ string, args ...string) ([]byte, error) {
				if strings.Join(args, " ") == tc.failedCommand {
					return nil, errors.New("command failed")
				}
				if strings.Join(args, " ") == "debug config" {
					return []byte(tc.cfg), nil
				}
				if strings.Join(args, " ") == "models" {
					return []byte("zai/model"), nil
				}
				return []byte(""), nil
			}
			verifyConnection = func(context.Context, authprobe.Connection, *http.Client) (ports.AgentAuthStatus, domain.AgentAuthDiagnostic) {
				t.Fatal("must not verify a guessed route")
				return "", domain.AgentAuthDiagnostic{}
			}
			ctx, evidence := ports.WithAuthEvidence(t.Context())
			if got := verifySelectedConnection(ctx, "mimo"); got != ports.AgentAuthStatusConfigured {
				t.Fatalf("status=%s", got)
			}
			if steps := evidence.Diagnostics(); len(steps) != 1 || steps[0].Code != tc.wantCode {
				t.Fatalf("diagnostics=%+v, want %s", steps, tc.wantCode)
			}
		})
	}
}

func TestMiMoReportsUnsupportedProviderValidation(t *testing.T) {
	for _, cfg := range []string{
		`{"model":"other/model"}`,
		`{"model":"zai/model","provider":{"zai":{"npm":"custom-sdk"}}}`,
	} {
		t.Run(cfg, func(t *testing.T) {
			oldRun, oldVerify := runAuthProbe, verifyConnection
			t.Cleanup(func() { runAuthProbe, verifyConnection = oldRun, oldVerify })
			runAuthProbe = func(_ context.Context, _ string, args ...string) ([]byte, error) {
				switch strings.Join(args, " ") {
				case "debug config":
					return []byte(cfg), nil
				case "models":
					return []byte("other/model\nzai/model"), nil
				default:
					return nil, nil
				}
			}
			verifyConnection = func(context.Context, authprobe.Connection, *http.Client) (ports.AgentAuthStatus, domain.AgentAuthDiagnostic) {
				t.Fatal("unsupported route must not send a provider request")
				return "", domain.AgentAuthDiagnostic{}
			}
			ctx, evidence := ports.WithAuthEvidence(t.Context())
			if got := verifySelectedConnection(ctx, "mimo"); got != ports.AgentAuthStatusConfigured {
				t.Fatalf("status=%s", got)
			}
			if steps := evidence.Diagnostics(); len(steps) != 1 || steps[0].Method != "provider_validation" || steps[0].Code != "provider_unsupported" {
				t.Fatalf("diagnostics=%+v", steps)
			}
		})
	}
}

func TestMiMoReportsAnUnavailableRecentModelWithoutProbingAnotherProvider(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "model.json"), []byte(`{"recent":[{"providerID":"zai-coding-plan","modelID":"glm-5.2"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	oldRun, oldVerify := runAuthProbe, verifyConnection
	t.Cleanup(func() { runAuthProbe, verifyConnection = oldRun, oldVerify })
	runAuthProbe = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "debug config":
			return []byte(`{}`), nil
		case "debug paths":
			return []byte("state " + dir), nil
		case "models":
			return []byte("zai-coding-plan/glm-5.3\nmimo/mimo-auto"), nil
		default:
			t.Fatalf("unexpected command: %v", args)
			return nil, nil
		}
	}
	verifyConnection = func(context.Context, authprobe.Connection, *http.Client) (ports.AgentAuthStatus, domain.AgentAuthDiagnostic) {
		t.Fatal("must not verify a different model or provider")
		return "", domain.AgentAuthDiagnostic{}
	}
	ctx, evidence := ports.WithAuthEvidence(t.Context())
	if got := verifySelectedConnection(ctx, "mimo"); got != ports.AgentAuthStatusConfigured {
		t.Fatalf("status=%s", got)
	}
	steps := evidence.Diagnostics()
	if len(steps) != 1 || steps[0].Code != "model_selection_unresolved" {
		t.Fatalf("diagnostics=%+v", steps)
	}
}

func TestMiMoDoesNotReplaceAnUnavailableConfiguredModelWithARecentModel(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "model.json"), []byte(`{"recent":[{"providerID":"zai-coding-plan","modelID":"glm-5.3"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	oldRun, oldVerify := runAuthProbe, verifyConnection
	t.Cleanup(func() { runAuthProbe, verifyConnection = oldRun, oldVerify })
	runAuthProbe = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		switch strings.Join(args, " ") {
		case "debug config":
			return []byte(`{"model":"zai-coding-plan/unavailable"}`), nil
		case "debug paths":
			return []byte("state " + dir), nil
		case "models":
			return []byte("zai-coding-plan/glm-5.3"), nil
		default:
			t.Fatalf("unexpected command: %v", args)
			return nil, nil
		}
	}
	t.Setenv("ZHIPU_API_KEY", "test-key")
	verifyConnection = func(context.Context, authprobe.Connection, *http.Client) (ports.AgentAuthStatus, domain.AgentAuthDiagnostic) {
		t.Fatal("must not probe a recent model in place of an explicit configured model")
		return ports.AgentAuthStatusAuthorized, domain.AgentAuthDiagnostic{}
	}
	ctx, evidence := ports.WithAuthEvidence(t.Context())
	if got := verifySelectedConnection(ctx, "mimo"); got != ports.AgentAuthStatusConfigured {
		t.Fatalf("status=%s", got)
	}
	if steps := evidence.Diagnostics(); len(steps) != 1 || steps[0].Code != "model_selection_unresolved" {
		t.Fatalf("diagnostics=%+v", steps)
	}
}
