package capability

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/providerprofile"
)

// profileInstallation 分离安装事实与原生认证；读取原生账号会被用例观察到。
type profileInstallation struct{ fakeReadiness }

// EnsureAgentInstallation 不把原生认证返回给自定义路由。
func (p *profileInstallation) EnsureAgentInstallation(context.Context, string) (domain.AgentInstallationState, error) {
	return p.installation, nil
}

// TestProfileReadinessUsesItsOwnCredential 删除 profile 分支会误拒无原生账号，或借用原生授权放行坏凭据。
func TestProfileReadinessUsesItsOwnCredential(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "Bearer profile-test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"test-model"}]}`))
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), providerprofile.Filename)
	raw, err := json.Marshal([]ports.ProviderProfile{{ID: "test", Name: "本地测试", Harness: domain.HarnessCodex, Model: "test-model", BaseURL: server.URL, CredentialEnv: "AO_WORKFLOW_CREDENTIAL_TEST"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	profiles := &providerprofile.Registry{Path: path, Transport: server.Client().Transport, LookupEnv: func(string) (string, bool) { return "profile-test-key", true }}
	choices, err := profiles.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	readiness := &profileInstallation{fakeReadiness: fakeReadiness{installation: domain.AgentInstallationInstalled, authentication: domain.AgentAuthenticationUnauthorized}}
	driver := &fakeDriver{harness: domain.HarnessCodex, caps: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true}}
	probe := NewChatDriverProbe(nil, &fakeRegistry{drivers: map[domain.AgentHarness]ports.ChatDriver{domain.HarnessCodex: driver}}, readiness, time.Now, time.Minute, profiles)
	resolver := NewResolver()
	resolver.RegisterProbe(domain.HarnessCodex, probe)
	request := ports.CapabilityProbeRequest{Harness: domain.HarnessCodex, Profile: choices[0].Reference, Model: "test-model"}
	if d := resolver.Gate(context.Background(), request, domain.CapabilityFeatureStreaming); !d.Allowed {
		t.Fatalf("valid profile blocked by absent native account: %+v", d)
	}
	if readiness.calls != 0 {
		t.Fatal("profile consulted native authentication")
	}
	readiness.authentication = domain.AgentAuthenticationAuthorized
	profiles.LookupEnv = func(string) (string, bool) { return "wrong-profile-key", true }
	if d := resolver.Gate(context.Background(), request, domain.CapabilityFeatureStreaming); d.Allowed {
		t.Fatal("native authorization endorsed a rejected profile")
	}
	if readiness.calls != 0 {
		t.Fatal("rejected profile fell back to native authentication")
	}
	profiles.LookupEnv = func(string) (string, bool) { return "profile-test-key", true }
	readiness.installation = domain.AgentInstallationNotInstalled
	if d := resolver.Gate(context.Background(), request, domain.CapabilityFeatureStreaming); d.Allowed {
		t.Fatal("profile bypassed installation gate")
	}
	readiness.installation = domain.AgentInstallationInstalled
	request.Profile = ""
	request.Model = ""
	if d := resolver.Gate(context.Background(), request, domain.CapabilityFeatureStreaming); !d.Allowed || readiness.calls != 1 {
		t.Fatalf("native path changed: %+v calls=%d", d, readiness.calls)
	}
}
