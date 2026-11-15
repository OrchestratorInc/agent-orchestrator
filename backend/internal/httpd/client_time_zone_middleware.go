package httpd

import (
	"net/http"

	"github.com/aoagents/agent-orchestrator/backend/internal/clienttz"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/requestscope"
)

func clientTimeZoneMiddleware(rec *clienttz.Recorder) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if rec == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if zone := r.Header.Get(clienttz.Header); zone != "" && trustedTimeZoneReporter(r) {
				rec.Observe(r.Context(), zone)
			}
			next.ServeHTTP(w, r)
		})
	}
}

func trustedTimeZoneReporter(r *http.Request) bool {
	if !requestscope.IsLAN(r.Context()) {
		return true
	}
	switch requestscope.CredentialFrom(r.Context()).Kind {
	case requestscope.CredentialPassword, requestscope.CredentialAccount, requestscope.CredentialDevice:
		return true
	default:
		return false
	}
}
