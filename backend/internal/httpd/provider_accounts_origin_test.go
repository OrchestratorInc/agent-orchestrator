package httpd

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProviderAccountOriginBoundaryCoversCatalogueAndSessionChoice(t *testing.T) {
	paths := []string{
		"/api/v1/provider-accounts",
		"/api/v1/provider-accounts/login",
		"/api/v1/provider-accounts/login/attempt",
		"/api/v1/provider-accounts/account/primary",
		"/api/v1/provider-accounts/account/sign-out",
		"/api/v1/provider-accounts/account",
		"/api/v1/sessions/session/provider-account",
	}
	origins := []struct {
		origin  string
		allowed bool
	}{
		{"", true}, {"app://renderer", true}, {"http://127.0.0.1:5173", true},
		{"http://127.0.0.1:9876", false}, {"http://localhost:9876", false}, {"http://ao-preview.session.localhost:9876", false},
		{"https://example.test", false}, {"null", false}, {"app://renderer.evil", false},
	}
	for _, path := range paths {
		for _, origin := range origins {
			for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, "OPTIONS"} {
				t.Run(method+path+origin.origin, func(t *testing.T) {
					called := false
					handler := managedAccountOriginMiddleware([]string{"app://renderer", "http://127.0.0.1:5173"})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { called = true; w.WriteHeader(http.StatusNoContent) }))
					request := httptest.NewRequest(method, path, nil)
					request.Header.Set("Origin", origin.origin)
					response := httptest.NewRecorder()
					handler.ServeHTTP(response, request)
					if called != origin.allowed {
						t.Fatalf("handler executed=%v allowed=%v status=%d", called, origin.allowed, response.Code)
					}
					if !origin.allowed && response.Code != http.StatusForbidden {
						t.Fatalf("unsafe origin status=%d", response.Code)
					}
				})
			}
		}
	}
}
func TestProviderAccountOriginPathDoesNotCaptureOtherSessionAPIs(t *testing.T) {
	for _, path := range []string{"/api/v1/sessions/s", "/api/v1/sessions/s/conversation", "/api/v1/sessions/s/provider-accounting", "/api/v1/provider-accounts-other", "/health"} {
		t.Run(path, func(t *testing.T) {
			called := false
			handler := managedAccountOriginMiddleware([]string{"app://renderer"})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { called = true; w.WriteHeader(http.StatusNoContent) }))
			request := httptest.NewRequest(http.MethodPost, path, nil)
			request.Header.Set("Origin", "http://preview.session.localhost:9876")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if !called || response.Code != http.StatusNoContent {
				t.Fatalf("ordinary daemon route restricted=%d", response.Code)
			}
		})
	}
}

func TestProviderAccountsCannotBeManagedThroughTheLANListener(t *testing.T) {
	paths := []string{
		"/api/v1/provider-accounts",
		"/api/v1/provider-accounts/",
		"/api/v1/provider-accounts/login",
		"/api/v1/provider-accounts/login/attempt",
		"/api/v1/provider-accounts/account",
		"/api/v1/provider-accounts/account/primary",
		"/api/v1/provider-accounts/account/sign-out",
		"/api/v1/sessions/owner/provider-account",
		"/api/v1/sessions/owner/provider-account/",
	}
	for _, path := range paths {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete, "OPTIONS"} {
			t.Run(method+path, func(t *testing.T) {
				called := false
				handler := lanControlBlock(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					called = true
					w.WriteHeader(http.StatusNoContent)
				}))
				request := httptest.NewRequest(method, path, nil)
				request.RemoteAddr = "192.168.1.42:12345"
				request.Host = "127.0.0.1:3001"
				request.Header.Set("Origin", "app://renderer")
				request.Header.Set("Authorization", "Bearer valid-mobile-password")
				request.Header.Set("X-Forwarded-For", "127.0.0.1")
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if called || response.Code != http.StatusNotFound {
					t.Fatalf("LAN account route executed=%v status=%d", called, response.Code)
				}
				if !isLANControlBlockedPath(path) {
					t.Fatal("account path was not classified as computer-only")
				}
			})
		}
	}
}
func TestProviderAccountLANBoundaryLeavesSessionAndModelReadsAvailable(t *testing.T) {
	for _, path := range []string{
		"/api/v1/sessions/owner",
		"/api/v1/sessions/owner/conversation",
		"/api/v1/sessions/owner/provider-accounting",
		"/api/v1/agents/codex/models",
		"/api/v1/agents/claude-code/models",
		"/api/v1/provider-accounts-summary",
		identityProbePath,
	} {
		t.Run(path, func(t *testing.T) {
			called := false
			handler := lanControlBlock(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			}))
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request.RemoteAddr = "192.168.1.42:12345"
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if !called || response.Code != http.StatusNoContent {
				t.Fatalf("unrelated mobile route blocked: %d", response.Code)
			}
		})
	}
}
