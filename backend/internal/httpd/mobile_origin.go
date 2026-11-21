package httpd

import (
	"net/http"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

// Mobile controls create remote access and disclose pairing credentials. They
// require exact renderer origins even when other APIs allow paired mobile clients.
// Run this before CORS so rejected preflights cannot acquire a browser grant.
func mobileControlOriginMiddleware(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := exactAllowedOrigins(allowedOrigins)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/mobile" && !strings.HasPrefix(r.URL.Path, "/api/v1/mobile/") {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Add("Vary", "Origin")
			w.Header().Add("Vary", "Sec-Fetch-Site")
			origins := r.Header.Values("Origin")
			trusted := len(origins) == 0 && r.Header.Get("Sec-Fetch-Site") == ""
			if len(origins) == 1 && origins[0] != "" {
				_, trusted = allowed[origins[0]]
			}
			if !trusted {
				envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "ORIGIN_FORBIDDEN", "Origin is not allowed to manage mobile access", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
