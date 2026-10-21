package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/go-chi/chi/v5"
)

// eventListStore records the types filter each ListClientEvents call got.
type eventListStore struct {
	Store
	types [][]string
}

func (s *eventListStore) ListClientEvents(_ context.Context, _ domain.Principal, _, _ string, _ int64, _ int, types []string) ([]domain.ClientEvent, bool, error) {
	s.types = append(s.types, types)
	return nil, false, nil
}

// The session events list takes an optional types filter, comma separated or
// repeated, and passes it to the store; without one every client event type
// is served. A filter too long is refused.
func TestReplayClientEventsTypesFilter(t *testing.T) {
	t.Parallel()
	const orgID, sessionID = "00000000-0000-4000-8000-0000000000a1", "00000000-0000-4000-8000-0000000000a2"
	replay := func(query string) (*httptest.ResponseRecorder, *eventListStore) {
		req := httptest.NewRequest(http.MethodGet, "/api/cloud/v1/orgs/"+orgID+"/sessions/"+sessionID+"/chat-events"+query, nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("orgId", orgID)
		rctx.URLParams.Add("sessionId", sessionID)
		ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
		req = req.WithContext(context.WithValue(ctx, principalKey, domain.Principal{UserID: "user-1"}))
		store := &eventListStore{}
		rec := httptest.NewRecorder()
		testServer(store).replayClientEvents(rec, req)
		return rec, store
	}
	for query, want := range map[string]string{
		"": "",
		"?types=workspace.setup,workspace.script.step":         "workspace.setup|workspace.script.step",
		"?types=workspace.setup&types=+workspace.script.step+": "workspace.setup|workspace.script.step",
		"?types=,": "",
	} {
		rec, store := replay(query)
		if rec.Code != http.StatusOK || len(store.types) != 1 || strings.Join(store.types[0], "|") != want {
			t.Fatalf("%q: status %d, types %q, want %q", query, rec.Code, store.types, want)
		}
	}
	if rec, store := replay("?types=" + strings.Repeat("a,", maxEventTypes+1)); rec.Code != http.StatusBadRequest || len(store.types) != 0 {
		t.Fatalf("too many types: status %d", rec.Code)
	}
}
