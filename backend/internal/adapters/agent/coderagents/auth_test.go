package coderagents

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/coderagents/coderapi"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeDeployment struct {
	token  string
	models string
}

func (f fakeDeployment) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get(coderapi.SessionTokenHeader) != f.token {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"unauthorized"}`))
		return
	}
	switch r.URL.Path {
	case "/api/v2/users/me":
		_, _ = w.Write([]byte(`{"id":"u1","username":"dev","email":"dev@example.com","organization_ids":["o1"]}`))
	case "/api/v2/users/me/organizations":
		_, _ = w.Write([]byte(`[{"id":"o1","name":"coder","display_name":"Coder","is_default":true}]`))
	case "/api/v2/organizations/o1/chats/models":
		_, _ = w.Write([]byte(f.models))
	default:
		http.NotFound(w, r)
	}
}

const oneModel = `{"models":[{"id":"m1","model":"glm-5","display_name":"GLM 5","enabled":true,"is_default":true,"reasoning_efforts":["low","high"]},{"id":"m2","model":"off","enabled":false}]}`

func authFixture(t *testing.T, deployment fakeDeployment) (*Plugin, map[string]string) {
	t.Helper()
	server := httptest.NewServer(deployment)
	t.Cleanup(server.Close)
	p := &Plugin{resolvedBinary: "/test/bin/coder", transport: server.Client().Transport}
	return p, map[string]string{coderapi.EnvURL: server.URL, coderapi.EnvSessionToken: "good"}
}

func TestAuthStatusAuthorizedNeedsSessionAndEnabledModel(t *testing.T) {
	p, env := authFixture(t, fakeDeployment{token: "good", models: oneModel})
	ctx, evidence := ports.WithAuthEvidence(context.Background())
	status, err := p.AuthStatusWithEnv(ctx, env)
	if err != nil || status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("AuthStatus = %q, %v; want authorized", status, err)
	}
	identity := evidence.Identity()
	if identity.Name != "dev" || identity.Organization != "Coder" || identity.Source != coderapi.EnvSessionToken {
		t.Fatalf("evidence = %+v", identity)
	}
	if identity.Method != domain.AgentAccountMethodToken {
		t.Fatalf("evidence method = %q", identity.Method)
	}
}

func TestAuthStatusWithoutEnabledModelIsOnlyConfigured(t *testing.T) {
	p, env := authFixture(t, fakeDeployment{token: "good", models: `{"models":[{"id":"m2","enabled":false}]}`})
	status, err := p.AuthStatusWithEnv(context.Background(), env)
	if err != nil || status != ports.AgentAuthStatusConfigured {
		t.Fatalf("AuthStatus = %q, %v; want configured", status, err)
	}
}

func TestAuthStatusRejectedSessionIsUnauthorized(t *testing.T) {
	p, env := authFixture(t, fakeDeployment{token: "other", models: oneModel})
	status, err := p.AuthStatusWithEnv(context.Background(), env)
	if err != nil || status != ports.AgentAuthStatusUnauthorized {
		t.Fatalf("AuthStatus = %q, %v; want unauthorized", status, err)
	}
}

func TestAuthStatusDeploymentWithoutChatsAPIIsOnlyConfigured(t *testing.T) {
	p, env := authFixture(t, fakeDeployment{token: "good", models: ""})
	// An empty models body decodes to no models; a 404 deployment is covered
	// by the driver's incompatibility check.
	status, err := p.AuthStatusWithEnv(context.Background(), env)
	if err != nil || status != ports.AgentAuthStatusConfigured {
		t.Fatalf("AuthStatus = %q, %v; want configured", status, err)
	}
}

func TestAuthStatusNotLoggedIn(t *testing.T) {
	p := &Plugin{resolvedBinary: "/test/bin/coder", runToken: func(context.Context, string, []string) ([]byte, []byte, error) {
		return nil, []byte("no session token found - run 'coder login' first"), errors.New("exit status 1")
	}}
	env := map[string]string{coderapi.EnvURL: "https://coder.example.com", coderapi.EnvSessionToken: ""}
	status, err := p.AuthStatusWithEnv(context.Background(), env)
	if err != nil || status != ports.AgentAuthStatusUnauthorized {
		t.Fatalf("AuthStatus = %q, %v; want unauthorized", status, err)
	}
}

func TestTokenCommandNeverSeesTokenInArgsAndPassesDeployment(t *testing.T) {
	server := httptest.NewServer(fakeDeployment{token: "from-cli", models: oneModel})
	defer server.Close()
	var gotEnv []string
	p := &Plugin{resolvedBinary: "/test/bin/coder", transport: server.Client().Transport,
		runToken: func(_ context.Context, binary string, env []string) ([]byte, []byte, error) {
			if binary != "/test/bin/coder" {
				t.Errorf("token command binary = %q", binary)
			}
			gotEnv = env
			return []byte("from-cli\n"), nil, nil
		}}
	status, err := p.AuthStatusWithEnv(context.Background(), map[string]string{coderapi.EnvURL: server.URL, coderapi.EnvSessionToken: ""})
	if err != nil || status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("AuthStatus = %q, %v", status, err)
	}
	found := false
	for _, entry := range gotEnv {
		if strings.HasPrefix(entry, coderapi.EnvURL+"=") {
			found = entry == coderapi.EnvURL+"="+server.URL
		}
	}
	if !found {
		t.Fatalf("token command did not target the configured deployment: %v", gotEnv)
	}
}

func TestVersionIdentityIsAnchored(t *testing.T) {
	for output, want := range map[string]bool{
		"Coder v2.37.9+f1205079 Fri Oct 30 2026\n\nhttps://github.com/coder/coder/commit/f1205079": true,
		"coder 1.2.3":              false,
		"Something Coder v2.37.9":  false,
		"Coder v1.9.0+old release": false,
	} {
		p := &Plugin{runVersion: func(context.Context, string) ([]byte, error) { return []byte(output), nil }}
		if ok := p.versionIdentified(context.Background(), "/test/bin/coder"); ok != want {
			t.Fatalf("version(%q) ok = %v, want %v", output, ok, want)
		}
	}
}

func TestGetLaunchCommandIsChatOnly(t *testing.T) {
	if _, err := New().GetLaunchCommand(context.Background(), ports.LaunchConfig{}); err == nil {
		t.Fatal("terminal launch accepted")
	}
}
