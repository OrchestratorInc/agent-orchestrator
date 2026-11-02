package httpd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/telemetrymeta"
)

func TestRequestSurfaceMiddleware(t *testing.T) {
	const renderer = "app://renderer"
	cases := []struct {
		name  string
		setup func(*http.Request) *http.Request
		want  domain.RequestSurface
	}{
		{"desktop renderer origin", func(r *http.Request) *http.Request { r.Header.Set("Origin", renderer); return r }, domain.RequestSurfaceDesktop},
		{"native client", func(r *http.Request) *http.Request { return r }, domain.RequestSurfaceCLI},
		{"unlisted origin is not desktop", func(r *http.Request) *http.Request { r.Header.Set("Origin", "http://localhost:3000"); return r }, domain.RequestSurfaceCLI},
		{"phone install header", func(r *http.Request) *http.Request { r.Header.Set(InstallIDHeader, "phone-1"); return r }, domain.RequestSurfaceMobile},
		{"authenticated LAN bearer", func(r *http.Request) *http.Request {
			r.Header.Set("Origin", "http://localhost")
			return r.WithContext(context.WithValue(r.Context(), lanBearerContextKey{}, true))
		}, domain.RequestSurfaceMobile},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got domain.RequestSurface
			var ok bool
			h := requestSurfaceMiddleware([]string{renderer})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got, ok = telemetrymeta.SurfaceFromContext(r.Context())
			}))
			h.ServeHTTP(httptest.NewRecorder(), tc.setup(httptest.NewRequest(http.MethodPost, "/api/v1/sessions/x/send", nil)))
			if !ok || got != tc.want {
				t.Fatalf("surface = %q (ok=%v), want %q", got, ok, tc.want)
			}
		})
	}
}
