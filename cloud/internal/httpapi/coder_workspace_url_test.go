package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox/coder"
	"github.com/go-chi/chi/v5"
)

const (
	byoPrefixedSessionID = "3f2a9c1e-7b4d-4e8a-9c21-5d6e7f8a9b0c"
	byoDefaultSessionID  = "00000000-0000-0000-0000-0000000000b1"
	managedCoderSession  = "00000000-0000-0000-0000-0000000000b2"
	freestyleSessionID   = "00000000-0000-0000-0000-0000000000b3"
)

// coderWorkspaceURLStore serves sessions as the postgres store projects them:
// only a sandbox on a bring-your-own Coder connection carries a workspace ref.
type coderWorkspaceURLStore struct {
	Store
	sessions []domain.Session
}

func (s *coderWorkspaceURLStore) ListSessions(context.Context, domain.Principal, string, string, *domain.Cursor, int) ([]domain.Session, bool, error) {
	return s.sessions, false, nil
}

func (s *coderWorkspaceURLStore) GetSession(_ context.Context, _ domain.Principal, _, sessionID string) (domain.Session, error) {
	for _, session := range s.sessions {
		if session.ID == sessionID {
			return session, nil
		}
	}
	return domain.Session{}, nil
}

func (s *coderWorkspaceURLStore) PRFactsBySession(context.Context, string, []string) (map[string][]contract.PRFacts, error) {
	return map[string][]contract.PRFacts{}, nil
}

func (s *coderWorkspaceURLStore) PullRequestsBySessions(context.Context, string, []string) (map[string][]domain.PullRequest, error) {
	return map[string][]domain.PullRequest{}, nil
}

func coderWorkspaceURLRequest(path string, params map[string]string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rctx := chi.NewRouteContext()
	for key, value := range params {
		rctx.URLParams.Add(key, value)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	return req.WithContext(context.WithValue(ctx, principalKey, domain.Principal{UserID: byocUserID}))
}

func TestSessionResponsesExposeCoderWorkspaceURLOnlyForBYOCoder(t *testing.T) {
	t.Parallel()
	store := &coderWorkspaceURLStore{sessions: []domain.Session{
		{
			ID: byoPrefixedSessionID, OrgID: byocOrgID, SandboxProvider: "coder",
			BYOCoderWorkspace: &domain.CoderWorkspaceRef{
				BaseURL: "https://coder.acme.example.com/", Owner: "dev", NamePrefix: "dev",
			},
		},
		{
			ID: byoDefaultSessionID, OrgID: byocOrgID, SandboxProvider: "coder",
			BYOCoderWorkspace: &domain.CoderWorkspaceRef{BaseURL: "https://coder.acme.example.com", Owner: "ao-bot"},
		},
		// AO's managed Coder and Freestyle never carry a workspace ref.
		{ID: managedCoderSession, OrgID: byocOrgID, SandboxProvider: "coder"},
		{ID: freestyleSessionID, OrgID: byocOrgID, SandboxProvider: "freestyle"},
	}}
	want := map[string]string{
		// The project prefix names the workspace <prefix>-<short session id>.
		byoPrefixedSessionID: "https://coder.acme.example.com/@dev/dev-3f2a9c1e7b4d",
		byoDefaultSessionID:  "https://coder.acme.example.com/@ao-bot/" + coder.WorkspaceName(byoDefaultSessionID),
		managedCoderSession:  "",
		freestyleSessionID:   "",
	}
	if got := coder.WorkspaceNameWithPrefix("dev", byoPrefixedSessionID); got != "dev-3f2a9c1e7b4d" {
		t.Fatalf("prefixed workspace name = %q; the expected URL above is stale", got)
	}

	rec := httptest.NewRecorder()
	testServer(store).listSessions(rec, coderWorkspaceURLRequest(
		"/api/cloud/v1/orgs/"+byocOrgID+"/sessions", map[string]string{"orgId": byocOrgID}))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d; body = %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.Items) != len(want) {
		t.Fatalf("list body = %s (%v)", rec.Body.String(), err)
	}
	for _, item := range list.Items {
		id, _ := item["id"].(string)
		got, present := item["coderWorkspaceUrl"]
		if want[id] == "" {
			if present {
				t.Errorf("list: session %s exposed coderWorkspaceUrl %v", id, got)
			}
			continue
		}
		if got != want[id] {
			t.Errorf("list: session %s coderWorkspaceUrl = %v, want %q", id, got, want[id])
		}
	}

	for id, wantURL := range want {
		rec := httptest.NewRecorder()
		testServer(store).getSession(rec, coderWorkspaceURLRequest(
			"/api/cloud/v1/orgs/"+byocOrgID+"/sessions/"+id,
			map[string]string{"orgId": byocOrgID, "sessionId": id}))
		if rec.Code != http.StatusOK {
			t.Fatalf("get %s status = %d; body = %s", id, rec.Code, rec.Body.String())
		}
		var body struct {
			Session sessionResponse `json:"session"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Session.CoderWorkspaceURL != wantURL {
			t.Errorf("get: session %s coderWorkspaceUrl = %q, want %q", id, body.Session.CoderWorkspaceURL, wantURL)
		}
	}
}

func TestCoderWorkspaceURLRejectsMalformedRecords(t *testing.T) {
	t.Parallel()
	for name, ref := range map[string]domain.CoderWorkspaceRef{
		"no owner":      {BaseURL: "https://coder.acme.example.com"},
		"no scheme":     {BaseURL: "coder.acme.example.com", Owner: "dev"},
		"non-http":      {BaseURL: "javascript:alert(1)", Owner: "dev"},
		"with userinfo": {BaseURL: "https://user:pw@coder.acme.example.com", Owner: "dev"},
	} {
		session := domain.Session{ID: byoDefaultSessionID, BYOCoderWorkspace: &ref}
		if got := toSessionResponse(session, nil).CoderWorkspaceURL; got != "" {
			t.Errorf("%s: coderWorkspaceUrl = %q, want none", name, got)
		}
	}
}
