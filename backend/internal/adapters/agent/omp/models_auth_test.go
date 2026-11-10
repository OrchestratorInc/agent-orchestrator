package omp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ompAuthTransport 在真实认证装配路径截获 HTTP，不接触外部供应商。
type ompAuthTransport func(*http.Request) (*http.Response, error)

func (f ompAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// TestOMPAuthStatusVerifiesModelsYAML 删除 models.yml 探测入口后，原生凭据和供应商均可用仍会返回 unknown。
func TestOMPAuthStatusVerifiesModelsYAML(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	writeFile(t, filepath.Join(dir, "config.yml"), "modelRoles:\n  default: custom/cn:test:high\n")
	writeFile(t, filepath.Join(dir, "models.yml"), `providers:
  custom:
    baseUrl: https://provider.example/v1
    api: openai-responses
    apiKey: "!native-credential-command"
    models:
      - id: cn:test
`)
	previousToken, previousTransport := ompTokenRunner, http.DefaultTransport
	t.Cleanup(func() { ompTokenRunner, http.DefaultTransport = previousToken, previousTransport })
	tokenCalls := 0
	ompTokenRunner = func(_ context.Context, binary string, args ...string) ([]byte, error) {
		tokenCalls++
		if binary != "omp" || !slices.Equal(args, []string{"token", "custom"}) {
			t.Fatal("authentication did not use the native provider token command")
		}
		return []byte("private-test-key\n"), nil
	}
	var credentials []string
	http.DefaultTransport = ompAuthTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.URL.String() != "https://provider.example/v1/models" {
			t.Fatal("authentication used a generation request or the wrong provider")
		}
		key := req.Header.Get("Authorization")
		credentials = append(credentials, key)
		status, body := http.StatusUnauthorized, `{}`
		if key == "Bearer private-test-key" {
			status, body = http.StatusOK, `{"data":[{"id":"cn:test"}]}`
		}
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	ctx, evidence := ports.WithAuthEvidence(t.Context())
	status, err := (&Plugin{resolvedBinary: "omp"}).AuthStatus(ctx)
	if err != nil || status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("models.yml authentication = %s, error = %v", status, err)
	}
	if tokenCalls != 1 || len(credentials) != 3 || credentials[0] != "" || credentials[1] == credentials[2] {
		t.Fatal("authentication did not require anonymous and invalid-credential controls")
	}
	identity := evidence.Identity()
	if identity.Provider != "custom" || !strings.HasSuffix(identity.Source, "models.yml") {
		t.Fatalf("wrong credential identity: %+v", identity)
	}
	diagnostics, _ := json.Marshal(evidence.Diagnostics())
	if strings.Contains(string(diagnostics), "private-test-key") || strings.Contains(string(diagnostics), "native-credential-command") {
		t.Fatal("credential or configured command leaked into diagnostics")
	}
}
