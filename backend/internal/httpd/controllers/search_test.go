package controllers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
)

type fakeConversationSearch struct {
	query string
	hits  []domain.ChatSearchHit
}

func (f *fakeConversationSearch) SearchConversationMessages(_ context.Context, query string) ([]domain.ChatSearchHit, error) {
	f.query = query
	return f.hits, nil
}

func searchConversations(t *testing.T, search controllers.ConversationSearch, target string) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	(&controllers.ConversationsController{Search: search}).Register(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func TestConversationSearchFlattensSnippets(t *testing.T) {
	search := &fakeConversationSearch{hits: []domain.ChatSearchHit{{
		MessageID: "m1", SessionID: "ao-1", ProjectID: "p1", Title: "Fix login",
		Snippet: "…we \x02closed\x03 the\n\n\x02palette\x03",
	}}}
	rec := searchConversations(t, search, "/conversations/search?query=close+palette")
	if rec.Code != http.StatusOK || search.query != "close palette" {
		t.Fatalf("status = %d query=%q body=%s", rec.Code, search.query, rec.Body)
	}
	var got controllers.ConversationSearchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := controllers.ConversationSearchHitResponse{MessageID: "m1", SessionID: "ao-1", ProjectID: "p1", Title: "Fix login", Text: "…we \x02closed\x03 the \x02palette\x03"}
	if len(got.Results) != 1 || got.Results[0] != want {
		t.Fatalf("results = %#v", got.Results)
	}
}

func TestConversationSearchValidation(t *testing.T) {
	if rec := searchConversations(t, &fakeConversationSearch{}, "/conversations/search?query=%20"); rec.Code != http.StatusBadRequest {
		t.Fatalf("blank query = %d", rec.Code)
	}
	if rec := searchConversations(t, nil, "/conversations/search?query=x"); rec.Code != http.StatusNotImplemented {
		t.Fatalf("no index = %d", rec.Code)
	}
}
