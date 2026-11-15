package controllers_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

type fakeConversationFinder struct {
	controllers.ConversationService
	err               error
	id, query, cursor string
}

func (f *fakeConversationFinder) FindConversation(_ context.Context, id, query, cursor string, _ ...chatsvc.FindAnchor) (chatsvc.FindResult, error) {
	f.id, f.query, f.cursor = id, query, cursor
	return chatsvc.FindResult{ConversationID: id, TranscriptRevision: "rev", Occurrences: []chatsvc.FindOccurrence{}, Total: 0}, f.err
}
func (f *fakeConversationFinder) FindConversationWindow(_ context.Context, _ string, _ int64) (chatsvc.Snapshot, error) {
	return chatsvc.Snapshot{}, f.err
}

func TestFindConversationHTTP(t *testing.T) {
	for _, test := range []struct {
		name, path string
		err        error
		want       int
	}{
		{"literal", "/conversations/c/find?query=" + url.QueryEscape(".*[%_] CAFÉ") + "&cursor=opaque", nil, 200},
		{"empty", "/conversations/c/find?query=", nil, 200},
		{"too long", "/conversations/c/find?query=" + strings.Repeat("a", 513), nil, 400},
		{"stale", "/conversations/c/find?query=x", chatsvc.ErrFindCursor, 409},
		{"unsupported", "/conversations/c/find?query=x", chatsvc.ErrFindUnavailable, 501},
		{"failure", "/conversations/c/find?query=x", errors.New("private data"), 500},
		{"window", "/conversations/c/find-window?sequence=42", nil, 200},
		{"invalid sequence", "/conversations/c/find-window?sequence=-1", nil, 400},
		{"overflow sequence", "/conversations/c/find-window?sequence=9223372036854775807", nil, 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := &fakeConversationFinder{err: test.err}
			router := chi.NewRouter()
			(&controllers.ConversationsController{Svc: fake}).Register(router)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, test.path, nil))
			if recorder.Code != test.want {
				t.Fatalf("%d: %s", recorder.Code, recorder.Body.String())
			}
			if strings.Contains(recorder.Body.String(), "private data") {
				t.Fatal("internal error leaked")
			}
			if test.name == "literal" && (fake.id != "c" || fake.query != ".*[%_] CAFÉ" || fake.cursor != "opaque") {
				t.Fatalf("request changed: %#v", fake)
			}
		})
	}
}
