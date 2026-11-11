package httpd

import (
	"net/http"

	"github.com/aoagents/agent-orchestrator/backend/internal/clienttz"
)

// clientTimeZoneMiddleware records the time zone the desktop app reports, so
// agent prompts name the user's zone rather than this daemon's, which differs
// on a remote host. It never rejects a request; a missing or invalid header
// is ignored.
func clientTimeZoneMiddleware(rec *clienttz.Recorder) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if rec == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if zone := r.Header.Get(clienttz.Header); zone != "" {
				rec.Observe(zone)
			}
			next.ServeHTTP(w, r)
		})
	}
}
