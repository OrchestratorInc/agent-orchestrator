package httpd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSideScopedRequestsCannotDelegateOrControlAgents(t *testing.T) {
	handler := sideConversationPolicy(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, path := range []string{"/orchestrators", "/orchestrators/delegate", "/sessions/main/send", "/sessions/main/conversation/side-chats", "/agents", "/reviews/other", "/automations"} {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
			r := httptest.NewRequest(method, "/api/v1"+path, nil)
			r.Header.Set("X-AO-Side-Conversation", "side-1")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "SIDE_CHAT_DELEGATION_DISABLED") {
				t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
			}
			r.Header.Del("X-AO-Side-Conversation")
			w = httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != http.StatusNoContent {
				t.Fatal("main loopback behavior changed")
			}
		}
	}
	for _, path := range []string{"/api/v1/fs/read", "/api/v1/browser/status"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("X-AO-Side-Conversation", "side-1")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusNoContent {
			t.Fatal("ordinary exploration blocked")
		}
	}
}
