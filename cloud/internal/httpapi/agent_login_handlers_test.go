package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/agentconnect"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/secrets"
)

// Removing personal authentication from this route would reveal deployment
// capabilities to an unauthenticated caller.
func TestAgentLoginCapabilitiesRequirePersonalAuthentication(t *testing.T) {
	s := New(Options{})
	r := httptest.NewRequest(http.MethodGet, "/api/cloud/v1/me/agent-logins/capabilities", nil)
	w := httptest.NewRecorder()
	s.handler.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", w.Code, w.Body.String())
	}
}

func TestAgentLoginDisabledCapabilitiesRemainAnEmptyArray(t *testing.T) {
	s := New(Options{})
	w := httptest.NewRecorder()
	s.agentLoginCapabilities(w, httptest.NewRequest(http.MethodGet, "/me/agent-logins/capabilities", nil))
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"harnesses":[]}` {
		t.Fatalf("disabled capabilities: %d %s", w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("login capability response can be cached")
	}
}

type loginAPIUserStore struct{ Store }

func (s *loginAPIUserStore) PrincipalFromLocalToken(_ context.Context, hash []byte) (domain.Principal, error) {
	good := sha256.Sum256([]byte("ao_local_user"))
	if !bytes.Equal(hash, good[:]) {
		return domain.Principal{}, postgres.ErrNotFound
	}
	return domain.Principal{UserID: "7471762a-d5e7-442f-9f58-e9490195179d", Provider: "local"}, nil
}

type loginAPIRepo struct {
	agentconnect.Store
	attempt agentconnect.Attempt
}

func (s *loginAPIRepo) CreateAgentLogin(_ context.Context, _ domain.Principal, a agentconnect.Attempt, _ int) (agentconnect.Attempt, bool, error) {
	if s.attempt.IdempotencyKey == a.IdempotencyKey {
		return s.attempt, false, nil
	}
	s.attempt = a
	return a, true, nil
}
func (s *loginAPIRepo) SetAgentLoginJob(_ context.Context, _, _, job string) (bool, error) {
	s.attempt.JobID = job
	return true, nil
}
func (s *loginAPIRepo) FindAgentLoginRunner(_ context.Context, id string, hash []byte) (agentconnect.Attempt, error) {
	if id != s.attempt.ID || !bytes.Equal(hash, s.attempt.TokenHash) {
		return agentconnect.Attempt{}, agentconnect.ErrNotFound
	}
	return s.attempt, nil
}
func (s *loginAPIRepo) GetAgentLogin(_ context.Context, user, id string) (agentconnect.Attempt, error) {
	if user != s.attempt.UserID || id != s.attempt.ID {
		return agentconnect.Attempt{}, agentconnect.ErrNotFound
	}
	return s.attempt, nil
}
func (s *loginAPIRepo) ConsumeAgentLoginCode(_ context.Context, _, _ string, _ []byte) (agentconnect.Attempt, error) {
	return s.attempt, nil
}
func (s *loginAPIRepo) UpdateAgentLogin(_ context.Context, _ agentconnect.Attempt, u agentconnect.Update, _, _ []byte) error {
	if !agentconnect.Active(s.attempt.Status) {
		return agentconnect.ErrConflict
	}
	s.attempt.Status = u.Status
	s.attempt.AuthorizationURL = u.AuthorizationURL
	return nil
}
func (s *loginAPIRepo) CancelAgentLogin(_ context.Context, user, id string) (agentconnect.Attempt, error) {
	if user != s.attempt.UserID || id != s.attempt.ID {
		return agentconnect.Attempt{}, agentconnect.ErrNotFound
	}
	s.attempt.Status = "cancelled"
	return s.attempt, nil
}

type loginAPIRunner struct {
	launch agentconnect.Launch
	starts int
}

func (r *loginAPIRunner) Start(_ context.Context, l agentconnect.Launch) (string, error) {
	r.launch = l
	r.starts++
	return l.ID, nil
}
func (r *loginAPIRunner) Stop(context.Context, string) error { return nil }

type loginValidationFunc func(context.Context, string, string, []byte) error

func (v loginValidationFunc) Validate(c context.Context, h, t string, b []byte) error {
	return v(c, h, t, b)
}

func loginAPIServer(t *testing.T, validator credentialValidator) (*Server, *loginAPIRepo, *loginAPIRunner) {
	t.Helper()
	cipher, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repo := &loginAPIRepo{}
	runner := &loginAPIRunner{}
	s := New(Options{Store: &loginAPIUserStore{}, LocalAuthEnabled: true, AgentLogins: agentconnect.New(agentconnect.Config{Store: repo, Cipher: cipher, Runner: runner}), CredentialValidator: validator, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	return s, repo, runner
}
func loginAPIRequest(s *Server, method, path, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/api/cloud/v1"+path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Idempotency-Key", "6193d5fa-e976-47db-937c-bb583d68c6aa")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestAgentLoginHTTPContractSeparatesRunnerAndPersonalAuthorization(t *testing.T) {
	s, repo, runner := loginAPIServer(t, nil)
	for range 2 {
		w := loginAPIRequest(s, http.MethodPost, "/me/agent-logins", "ao_local_user", `{"orgId":"70736960-e437-4405-bea9-e4679667033a","harness":"codex"}`)
		if w.Code != 201 {
			t.Fatalf("create: %d %s", w.Code, w.Body.String())
		}
		var body struct {
			Login agentconnect.Login `json:"login"`
		}
		if json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Login.ID == "" {
			t.Fatalf("public contract: %s", w.Body.String())
		}
		if strings.Contains(w.Body.String(), runner.launch.Token) || strings.Contains(w.Body.String(), "TokenHash") {
			t.Fatal("public start leaked runner credential")
		}
	}
	if runner.starts != 1 {
		t.Fatalf("idempotent POST launched %d jobs", runner.starts)
	}
	path := "/agent-logins/" + repo.attempt.ID + "/runner"
	if w := loginAPIRequest(s, http.MethodGet, path, "ao_local_user", ""); w.Code != 404 {
		t.Fatalf("ordinary user accessed runner: %d %s", w.Code, w.Body.String())
	}
	if w := loginAPIRequest(s, http.MethodGet, path, runner.launch.Token, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"harness":"codex"`) {
		t.Fatalf("runner poll: %d %s", w.Code, w.Body.String())
	}
	if w := loginAPIRequest(s, http.MethodPost, path, runner.launch.Token, `{"status":"waiting","authorizationUrl":"https://attacker.test/login"}`); w.Code != 422 {
		t.Fatalf("unsafe provider URL accepted: %d %s", w.Code, w.Body.String())
	}
	if w := loginAPIRequest(s, http.MethodPost, path, runner.launch.Token, `{"status":"waiting","authorizationUrl":"https://auth.openai.com/codex/device","userCode":"ABCD-EFGH"}`); w.Code != 204 {
		t.Fatalf("runner status: %d %s", w.Code, w.Body.String())
	}
	if w := loginAPIRequest(s, http.MethodDelete, "/me/agent-logins/"+repo.attempt.ID, "ao_local_user", ""); w.Code != 204 {
		t.Fatalf("cancel: %d %s", w.Code, w.Body.String())
	}
	if w := loginAPIRequest(s, http.MethodPost, path, runner.launch.Token, `{"status":"waiting"}`); w.Code != 409 {
		t.Fatalf("late runner write: %d %s", w.Code, w.Body.String())
	}
}

func TestAgentLoginValidationFailureNeverEchoesProviderOrSecretText(t *testing.T) {
	const credential = `{"tokens":{"access_token":"access-secret","refresh_token":"refresh-secret"}}`
	s, repo, runner := loginAPIServer(t, loginValidationFunc(func(context.Context, string, string, []byte) error {
		return errors.New("provider echoed " + credential)
	}))
	var logs bytes.Buffer
	s.logger = slog.New(slog.NewTextHandler(&logs, nil))
	if w := loginAPIRequest(s, http.MethodPost, "/me/agent-logins", "ao_local_user", `{"orgId":"70736960-e437-4405-bea9-e4679667033a","harness":"codex"}`); w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	data, _ := json.Marshal(agentconnect.Update{Status: "connected", CredentialType: "auth_json", Secret: credential})
	w := loginAPIRequest(s, http.MethodPost, "/agent-logins/"+repo.attempt.ID+"/runner", runner.launch.Token, string(data))
	if w.Code != 502 || strings.Contains(w.Body.String()+logs.String(), "access-secret") || strings.Contains(w.Body.String()+logs.String(), "provider echoed") || repo.attempt.Status == "connected" {
		t.Fatalf("unsafe validation response: %d %s logs=%s", w.Code, w.Body.String(), logs.String())
	}
}
