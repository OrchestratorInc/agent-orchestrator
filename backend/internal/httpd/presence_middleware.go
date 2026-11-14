package httpd

import (
	"net/http"

	"github.com/aoagents/agent-orchestrator/backend/internal/presence"
)

const InstallIDHeader = "X-AO-Install-Id"

func presenceMiddleware(t *presence.Tracker) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if t == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Touch(r.Header.Get(InstallIDHeader))
			next.ServeHTTP(w, r)
		})
	}
}
