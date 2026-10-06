package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
	"github.com/go-chi/chi/v5"
)

const (
	byocOrgID      = "00000000-0000-0000-0000-0000000000a1"
	byocProjectID  = "00000000-0000-0000-0000-0000000000d4"
	byocUserID     = "00000000-0000-0000-0000-0000000000f6"
	byocConnID     = "99999999-9999-9999-9999-999999999999"
	byocTemplate   = "3b3f373d-c42d-5313-a57e-b2abe56e2fe3"
	byocBaseURL    = "https://org-coder.example.com"
	byocOwner      = "org-bot"
	byocAgentName  = "org-dev"
	byocDurableDir = "/srv/org-coder"
)

// stubCoderBYOCStore implements the provider-connection and session methods
// createSession reaches when a coder session's org has a bring-your-own Coder
// connection.
type stubCoderBYOCStore struct {
	Store
	captured                    domain.CreateSession
	created                     bool
	personalCredentialAvailable bool
	// omitOrgTemplate drops the template from the org's bring-your-own config, the
	// shape after the slimmed settings form (template chosen per project instead).
	omitOrgTemplate bool
	// projectConfig is the project's stored Config JSON (its coder dev-kit config
	// lives under the "coder" key). Nil => a bare project with no coder config.
	projectConfig json.RawMessage
}

func (s *stubCoderBYOCStore) UserAgentCredentialAvailable(
	_ context.Context, _, _ string,
) (bool, error) {
	return s.personalCredentialAvailable, nil
}

func (s *stubCoderBYOCStore) GetProject(
	_ context.Context, _ domain.Principal, _, projectID string,
) (domain.Project, error) {
	return domain.Project{ID: projectID, Config: s.projectConfig}, nil
}

func (s *stubCoderBYOCStore) ListProviderConnections(
	_ context.Context, _ domain.Principal, _ string,
) ([]domain.ProviderConnection, error) {
	orgTemplate := byocTemplate
	if s.omitOrgTemplate {
		orgTemplate = ""
	}
	orgCoder, _ := domain.EncodeOrgCoderConfig(domain.OrgCoderConfig{
		BaseURL: byocBaseURL, Owner: byocOwner, TemplateID: orgTemplate,
		AgentName: byocAgentName, DurableRoot: byocDurableDir,
		Parameters: map[string]string{"region": "eu"},
	})
	return []domain.ProviderConnection{
		// A legacy org-scoped coding-agent credential must not authorize launch.
		{ID: "agent-1", Provider: "claude-code", Label: "default", ValidationState: "valid"},
		{ID: byocConnID, Provider: sandbox.ProviderCoder, Label: "default", Config: orgCoder, ValidationState: "valid"},
	}, nil
}

func (s *stubCoderBYOCStore) UpsertProviderConnection(
	context.Context, domain.Principal, string, string, string, []byte, []byte, json.RawMessage,
) (domain.ProviderConnection, error) {
	return domain.ProviderConnection{}, nil
}

func (s *stubCoderBYOCStore) DeleteProviderConnection(
	context.Context, domain.Principal, string, string, string,
) error {
	return nil
}

func (s *stubCoderBYOCStore) CreateSession(
	_ context.Context, _ domain.Principal, _, _ string, _ int, input domain.CreateSession,
) (domain.Session, error) {
	s.created = true
	s.captured = input
	return domain.Session{ID: "00000000-0000-0000-0000-0000000000e5", Kind: input.Kind}, nil
}

func newBYOCServer(store Store) *Server {
	return New(Options{
		Store:                     store,
		SandboxProvider:           sandbox.ProviderCoder,
		AvailableSandboxProviders: []string{sandbox.ProviderCoder},
		Provisioning:              bothProviderProvisioning(sandbox.ProviderCoder),
		Logger:                    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func byocCreateSessionRequest(t *testing.T) *http.Request {
	t.Helper()
	body := `{"projectId":"` + byocProjectID + `","kind":"orchestrator","harness":"claude-code","displayName":"org-session","prompt":"do the work","mode":"trusted","provider":"coder"}`
	req := httptest.NewRequest(http.MethodPost, "/api/cloud/v1/orgs/"+byocOrgID+"/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "22222222-2222-2222-2222-222222222222")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("orgId", byocOrgID)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, principalKey, domain.Principal{UserID: byocUserID})
	return req.WithContext(ctx)
}

// When the org has a bring-your-own Coder connection, a coder session binds to it
// and provisions against the org's deployment, not the env default.
func TestCreateSessionBindsOrgCoderConnection(t *testing.T) {
	t.Parallel()
	store := &stubCoderBYOCStore{personalCredentialAvailable: true}
	srv := newBYOCServer(store)

	rec := httptest.NewRecorder()
	srv.createSession(rec, byocCreateSessionRequest(t))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	if !store.created {
		t.Fatal("CreateSession was not called")
	}
	if store.captured.SandboxConnectionID != byocConnID {
		t.Fatalf("SandboxConnectionID = %q, want %q", store.captured.SandboxConnectionID, byocConnID)
	}
	profile, err := sandbox.DecodeCoderSessionProfile(store.captured.ResourceProfile)
	if err != nil {
		t.Fatalf("decode stamped profile: %v", err)
	}
	if profile.BaseURL != byocBaseURL || profile.Owner != byocOwner ||
		profile.TemplateID != byocTemplate || profile.AgentName != byocAgentName ||
		profile.DurableRoot != byocDurableDir || profile.Parameters["region"] != "eu" {
		t.Fatalf("stamped profile did not use the org override: %+v", profile)
	}
}

// A bring-your-own-Coder org whose config carries no template still runs: the
// template comes from the project's dev-kit config, chosen per project.
func TestCreateSessionUsesProjectTemplateWhenOrgHasNone(t *testing.T) {
	t.Parallel()
	projectConfig, err := domain.MergeProjectCoderConfig(nil, domain.ProjectCoderConfig{TemplateID: byocTemplate})
	if err != nil {
		t.Fatalf("build project config: %v", err)
	}
	store := &stubCoderBYOCStore{personalCredentialAvailable: true, omitOrgTemplate: true, projectConfig: projectConfig}
	srv := newBYOCServer(store)

	rec := httptest.NewRecorder()
	srv.createSession(rec, byocCreateSessionRequest(t))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	profile, err := sandbox.DecodeCoderSessionProfile(store.captured.ResourceProfile)
	if err != nil {
		t.Fatalf("decode stamped profile: %v", err)
	}
	if profile.TemplateID != byocTemplate || profile.Owner != byocOwner {
		t.Fatalf("project template not applied over a templateless org: %+v", profile)
	}
}

// When neither the org config nor the project supplies a template, the session
// create fails with a clear, user-fixable 422 rather than a 500.
func TestCreateSessionRequiresTemplateWhenNeitherOrgNorProjectHasOne(t *testing.T) {
	t.Parallel()
	store := &stubCoderBYOCStore{personalCredentialAvailable: true, omitOrgTemplate: true}
	srv := newBYOCServer(store)

	rec := httptest.NewRecorder()
	srv.createSession(rec, byocCreateSessionRequest(t))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", rec.Code, rec.Body.String())
	}
	if store.created {
		t.Fatal("a session with no resolvable template must not be created")
	}
	if !strings.Contains(rec.Body.String(), "coder_template_required") {
		t.Fatalf("expected coder_template_required error, got %s", rec.Body.String())
	}
}

func TestCreateSessionRejectsOrgOnlyAgentCredential(t *testing.T) {
	t.Parallel()
	store := &stubCoderBYOCStore{}
	srv := newBYOCServer(store)
	rec := httptest.NewRecorder()
	srv.createSession(rec, byocCreateSessionRequest(t))
	if rec.Code != http.StatusUnprocessableEntity || store.created {
		t.Fatalf("org-only launch: status=%d created=%v body=%s", rec.Code, store.created, rec.Body.String())
	}
}
