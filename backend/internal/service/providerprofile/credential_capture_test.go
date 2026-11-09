package providerprofile

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestCaptureEnvironmentKeepsAuthenticationPrivate 删除启动接管或清除会使凭据失效或继续向默认子进程泄漏。
func TestCaptureEnvironmentKeepsAuthenticationPrivate(t *testing.T) {
	r, ref := authFixture(t, func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "Bearer fake-private-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"test-model"}]}`))
	})
	t.Setenv("AO_WORKFLOW_CREDENTIAL_TEST", "fake-private-key")
	t.Setenv("ao_workflow_credential_other", "other-fake-key")
	t.Setenv(ports.ProviderProfileKeyEnv, "stale-forwarded-key")
	t.Setenv("OPENAI_API_KEY", "native-fake-key")
	private, err := CaptureEnvironment(r.Path)
	if err != nil {
		t.Fatal(err)
	}
	private.Transport = r.Transport
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(strings.ToUpper(key), ports.ProviderProfileCredentialPrefix) || strings.EqualFold(key, ports.ProviderProfileKeyEnv) {
			t.Fatal("daemon retained a platform credential in inherited environment")
		}
	}
	if os.Getenv("OPENAI_API_KEY") != "native-fake-key" {
		t.Fatal("native authentication changed")
	}
	env, err := private.Environment(t.Context(), ref, domain.HarnessCodex, "test-model", "")
	if err != nil || env[ports.ProviderProfileKeyEnv] != "fake-private-key" {
		t.Fatal("private credential no longer authenticates selected provider")
	}
}
