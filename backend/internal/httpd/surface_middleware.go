package httpd

import (
	"net/http"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/telemetrymeta"
)

func requestSurfaceMiddleware(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := exactAllowedOrigins(allowedOrigins)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			surface := requestSurface(r, allowed)
			next.ServeHTTP(w, r.WithContext(telemetrymeta.WithSurface(r.Context(), surface)))
		})
	}
}

func requestSurface(r *http.Request, allowedOrigins map[string]struct{}) domain.RequestSurface {
	if authenticatedLANBearer(r) || r.Header.Get(InstallIDHeader) != "" {
		return domain.RequestSurfaceMobile
	}
	if _, ok := allowedOrigins[r.Header.Get("Origin")]; ok {
		return domain.RequestSurfaceDesktop
	}
	return domain.RequestSurfaceCLI
}
