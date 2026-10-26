package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

type participantsTestStore struct {
	Store
	asked []string
}

func (s *participantsTestStore) SessionParticipants(_ context.Context, _ domain.Principal, _ string, sessionIDs []string) (map[string][]domain.SessionParticipant, error) {
	s.asked = sessionIDs
	return map[string][]domain.SessionParticipant{
		composerTestSession: {
			{UserID: "u-owner", Name: "Vaibhaav", AvatarURL: "https://lh3.googleusercontent.com/owner", Role: "owner", State: "idle"},
			{UserID: "u-guest", Name: "Vaanya", Role: "editor", Present: true, State: "working"},
		},
	}, nil
}

func participantsRequest(t *testing.T, store Store, query string) *httptest.ResponseRecorder {
	t.Helper()
	server := testServer(store)
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey, domain.Principal{UserID: composerTestUser})))
		})
	})
	router.Get("/orgs/{orgId}/session-participants", server.listSessionParticipants)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/orgs/"+composerTestOrg+"/session-participants"+query, nil))
	return recorder
}

// A board asks for its sessions at once and hears back only the shared ones,
// in the order it asked.
func TestListSessionParticipantsRoute(t *testing.T) {
	t.Parallel()
	unshared := "11111111-2222-4333-8444-555555555555"
	store := &participantsTestStore{}
	recorder := participantsRequest(t, store, "?sessionId="+unshared+"&sessionId="+composerTestSession)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	want := `{"sessions":[{"sessionId":"` + composerTestSession + `","participants":[` +
		`{"userId":"u-owner","name":"Vaibhaav","avatarUrl":"https://lh3.googleusercontent.com/owner","role":"owner","present":false,"state":"idle"},` +
		`{"userId":"u-guest","name":"Vaanya","role":"editor","present":true,"state":"working"}]}]}`
	if got := strings.TrimSpace(recorder.Body.String()); got != want {
		t.Fatalf("body\n got %s\nwant %s", got, want)
	}
	if len(store.asked) != 2 {
		t.Fatalf("asked the store for %v", store.asked)
	}

	if code := participantsRequest(t, store, "?sessionId=not-a-uuid").Code; code != http.StatusBadRequest {
		t.Fatalf("a malformed session id: %d, want 400", code)
	}
	tooMany := strings.Repeat("&sessionId="+composerTestSession, 101)
	if code := participantsRequest(t, store, "?"+tooMany[1:]).Code; code != http.StatusBadRequest {
		t.Fatalf("101 sessions: %d, want 400", code)
	}
}
