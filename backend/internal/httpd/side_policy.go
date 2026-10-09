package httpd

import (
	"net/http"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

// This restricts requests made with side-scoped AO command context. It does
// not authenticate the primary loopback listener or sandbox arbitrary shell
// programs that discard that context.
func sideConversationPolicy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-AO-Side-Conversation") != "" {
			path := strings.TrimPrefix(r.URL.Path, "/api/v1")
			for _, prefix := range []string{"/sessions", "/orchestrators", "/agents", "/reviews", "/reports", "/automations", "/side-chats"} {
				if path == prefix || strings.HasPrefix(path, prefix+"/") {
					envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "SIDE_CHAT_DELEGATION_DISABLED", "Side conversations cannot inspect or control other agents or conversations.", nil)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}
