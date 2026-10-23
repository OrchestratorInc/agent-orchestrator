package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
	"github.com/go-chi/chi/v5"
)

type renameStore struct {
	Store
	renamed string
	err     error
}

func (s *renameStore) RenameSession(_ context.Context, _ domain.Principal, _, sessionID, name string) (domain.Session, error) {
	if s.err != nil {
		return domain.Session{}, s.err
	}
	s.renamed = name
	return domain.Session{ID: sessionID, DisplayName: name}, nil
}

// A session is renamed as the local daemon renames one: a trimmed name of 1
// to 100 characters, with the daemon's error codes, and never the project
// orchestrator.
func TestUpdateSessionRenames(t *testing.T) {
	const sessionID = "00000000-0000-0000-0000-0000000000e1"
	for _, tc := range []struct {
		name, body, want, code string
		storeErr               error
		status                 int
	}{
		{"renamed", `{"displayName":"  Fix the login page  "}`, "Fix the login page", "", nil, http.StatusOK},
		{"empty", `{"displayName":"   "}`, "", "DISPLAY_NAME_REQUIRED", nil, http.StatusUnprocessableEntity},
		{"missing", `{}`, "", "DISPLAY_NAME_REQUIRED", nil, http.StatusUnprocessableEntity},
		{"too long", `{"displayName":"` + strings.Repeat("é", 101) + `"}`, "", "DISPLAY_NAME_TOO_LONG", nil, http.StatusUnprocessableEntity},
		{"unknown field", `{"name":"Fix"}`, "", "invalid_request", nil, http.StatusBadRequest},
		{"orchestrator", `{"displayName":"Fix"}`, "", "ORCHESTRATOR_RENAME_UNSUPPORTED", postgres.ErrOrchestratorRename, http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &renameStore{err: tc.storeErr}
			srv := newChildServer(store, bothProviderProvisioning(sandbox.ProviderNodeOps), sandbox.ProviderNodeOps)
			req := httptest.NewRequest(http.MethodPatch, "/api/cloud/v1/orgs/"+autolinkOrgID+"/sessions/"+sessionID, strings.NewReader(tc.body))
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("orgId", autolinkOrgID)
			rctx.URLParams.Add("sessionId", sessionID)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
			recorder := httptest.NewRecorder()
			srv.updateSession(recorder, req)
			if recorder.Code != tc.status || store.renamed != tc.want || !strings.Contains(recorder.Body.String(), tc.code) {
				t.Fatalf("status=%d renamed=%q body=%s", recorder.Code, store.renamed, recorder.Body)
			}
		})
	}
}
