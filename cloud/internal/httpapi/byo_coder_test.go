package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
	"github.com/go-chi/chi/v5"
)

func TestPutOrgCoderConfigStoresStartupPolicy(t *testing.T) {
	t.Parallel()
	store := &coderConfigFakeStore{}
	srv, _ := newCoderConfigServer(t, store, false)
	body := `{"token":"` + coderCfgToken + `","baseUrl":"https://coder.acme.example.com","owner":"ao-bot",` +
		`"requireMountedDurableRoot":true,"startupTimeoutSeconds":900}`
	rec := httptest.NewRecorder()
	srv.putOrgCoderConfig(rec, coderConfigRequest(t, http.MethodPut, body, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	cfg, err := domain.DecodeOrgCoderConfig(store.lastConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.RequireMountedDurableRoot || cfg.StartupTimeoutSeconds != 900 {
		t.Fatalf("stored config = %+v, want strict mount and 900s", cfg)
	}
	// An omitted durable root resolves to the workspace user's home.
	if cfg.DurableRoot != sandbox.CoderHomeDurableRoot {
		t.Fatalf("durableRoot = %q, want %q", cfg.DurableRoot, sandbox.CoderHomeDurableRoot)
	}

	for _, timeout := range []string{"30", "86400", "-1"} {
		rec := httptest.NewRecorder()
		srv.putOrgCoderConfig(rec, coderConfigRequest(t, http.MethodPut,
			`{"token":"`+coderCfgToken+`","baseUrl":"https://coder.acme.example.com","owner":"ao-bot","startupTimeoutSeconds":`+timeout+`}`, nil))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("startupTimeoutSeconds=%s: status = %d, want 422", timeout, rec.Code)
		}
	}
}

// A BYO coder session inherits the connection's startup policy (lenient mount,
// 20-minute budget) and the project's workspace name prefix.
func TestCreateSessionStampsBYOStartupPolicyAndPrefix(t *testing.T) {
	t.Parallel()
	projectConfig, err := domain.MergeProjectCoderConfig(nil, domain.ProjectCoderConfig{
		TemplateID: byocTemplate, WorkspaceNamePrefix: "ahmad",
	})
	if err != nil {
		t.Fatal(err)
	}
	store := &stubCoderBYOCStore{personalCredentialAvailable: true, projectConfig: projectConfig}
	rec := httptest.NewRecorder()
	newBYOCServer(store).createSession(rec, byocCreateSessionRequest(t))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	profile, err := sandbox.DecodeCoderSessionProfile(store.captured.ResourceProfile)
	if err != nil {
		t.Fatal(err)
	}
	if profile.WorkspaceNamePrefix != "ahmad" || profile.StartupTimeoutSeconds != 1200 ||
		profile.RequireMountedDurableRoot == nil || *profile.RequireMountedDurableRoot {
		t.Fatalf("stamped profile = %+v, want prefix ahmad, 1200s, lenient mount", profile)
	}
}

func TestParseCoderConfigInputValidatesWorkspaceNamePrefix(t *testing.T) {
	t.Parallel()
	for prefix, ok := range map[string]bool{
		"": true, "ahmad": true, "team-11x": true, "a": true, strings.Repeat("a", 20): true,
		strings.Repeat("a", 21): false, "Ahmad": false, "1team": false, "team-": false,
		"team--a": false, "team_a": false, "-team": false,
	} {
		cfg, err := parseCoderConfigInput(&coderConfigInput{WorkspaceNamePrefix: prefix})
		if (err == nil) != ok {
			t.Errorf("prefix %q: err = %v, want ok=%v", prefix, err, ok)
		}
		if ok && cfg.WorkspaceNamePrefix != prefix {
			t.Errorf("prefix %q stored as %q", prefix, cfg.WorkspaceNamePrefix)
		}
	}
}

func TestSessionResponseExposesStartupError(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 8, 15, 26, 8, 0, time.FixedZone("x", 3600))
	encoded, err := json.Marshal(toSessionResponse(domain.Session{
		ID: "s", StartupErrorCode: sandbox.StartupErrorWorkspaceNotReady,
		StartupErrorMessage: "Your Coder workspace wasn't ready after 20 minutes.", StartupErrorAt: &at,
	}, nil))
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		StartupError *struct {
			Code, Message string
			At            time.Time
		} `json:"startupError"`
	}
	if err := json.Unmarshal(encoded, &body); err != nil {
		t.Fatal(err)
	}
	if body.StartupError == nil || body.StartupError.Code != "workspace_not_ready" ||
		body.StartupError.Message != "Your Coder workspace wasn't ready after 20 minutes." || !body.StartupError.At.Equal(at) {
		t.Fatalf("startupError = %+v in %s", body.StartupError, encoded)
	}
	if encoded, _ := json.Marshal(toSessionResponse(domain.Session{ID: "s"}, nil)); strings.Contains(string(encoded), "startupError") {
		t.Fatalf("a session without a startup error rendered one: %s", encoded)
	}
}

type startupRetryStore struct {
	Store
	retryErr error
	retried  int
}

func (s *startupRetryStore) RetrySessionStartup(context.Context, domain.Principal, string, string) error {
	s.retried++
	return s.retryErr
}

func (s *startupRetryStore) GetSession(_ context.Context, _ domain.Principal, orgID, sessionID string) (domain.Session, error) {
	return domain.Session{ID: sessionID, OrgID: orgID, DesiredState: "running", ObservedState: "provisioning"}, nil
}

func TestRetrySessionStartup(t *testing.T) {
	t.Parallel()
	const sessionID = "00000000-0000-0000-0000-0000000000e5"
	request := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost,
			"/api/cloud/v1/orgs/"+byocOrgID+"/sessions/"+sessionID+"/startup-retry", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("orgId", byocOrgID)
		rctx.URLParams.Add("sessionId", sessionID)
		ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
		return req.WithContext(context.WithValue(ctx, principalKey, domain.Principal{UserID: byocUserID}))
	}

	store := &startupRetryStore{}
	rec := httptest.NewRecorder()
	newBYOCServer(store).retrySessionStartup(rec, request())
	if rec.Code != http.StatusAccepted || store.retried != 1 {
		t.Fatalf("status = %d, retried = %d; want 202 and one retry. body = %s", rec.Code, store.retried, rec.Body.String())
	}
	var body struct {
		Session sessionResponse `json:"session"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Session.ID != sessionID {
		t.Fatalf("response session = %+v (%v)", body.Session, err)
	}

	conflict := &startupRetryStore{retryErr: postgres.ErrConflict}
	rec = httptest.NewRecorder()
	newBYOCServer(conflict).retrySessionStartup(rec, request())
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "startup_retry_unavailable") {
		t.Fatalf("conflict status = %d body = %s, want 409 startup_retry_unavailable", rec.Code, rec.Body.String())
	}

	missing := &startupRetryStore{retryErr: postgres.ErrNotFound}
	rec = httptest.NewRecorder()
	newBYOCServer(missing).retrySessionStartup(rec, request())
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing session status = %d, want 404", rec.Code)
	}
}

// The desktop creates projects with the prefix nested in the free-form config
// (config.coder.workspaceNamePrefix); an invalid one is rejected up front.
func TestValidateProjectCoderConfig(t *testing.T) {
	t.Parallel()
	for raw, ok := range map[string]bool{
		`{}`: true,
		`{"coder":{"templateId":"` + byocTemplate + `"}}`:           true,
		`{"coder":{"workspaceNamePrefix":"ahmad"}}`:                 true,
		`{"coder":{"workspaceNamePrefix":"Ahmad"}}`:                 false,
		`{"coder":{"workspaceNamePrefix":" ahmad"}}`:                false,
		`{"coder":{"workspaceNamePrefix":"way-too-long-prefix-x"}}`: false,
	} {
		if err := validateProjectCoderConfig(json.RawMessage(raw)); (err == nil) != ok {
			t.Errorf("validateProjectCoderConfig(%s) = %v, want ok=%v", raw, err, ok)
		}
	}
}
