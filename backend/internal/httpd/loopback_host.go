package httpd

import (
	"net"
	"net/http"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

// loopbackHostGuard closes DNS rebinding on the loopback listeners. A hostile
// page can rebind its own name to 127.0.0.1 and then make same-origin requests
// to the daemon: the browser sends no Origin header on a same-origin GET, so
// the CORS boundary alone cannot tell it from a native client. The one fact
// such a request cannot hide is its Host, which still names the attacker's
// domain. Only loopback addresses and *.localhost names (which RFC 6761 pins
// to loopback, so they cannot be rebound) may address this daemon.
//
// It wraps only the loopback listeners. The LAN listener and the managed
// tunnel are reached by LAN addresses and public hostnames, and authenticate
// every request instead.
func loopbackHostGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackRequestHost(r.Host) {
			envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "HOST_FORBIDDEN",
				"Host is not allowed to address this daemon", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// loopbackRequestHost reports whether a request Host names this machine's
// loopback interface. An empty Host (HTTP/1.0) passes: browsers always send
// one, so it cannot carry a rebinding attempt.
func loopbackRequestHost(raw string) bool {
	if raw == "" {
		return true
	}
	host := raw
	if h, _, err := net.SplitHostPort(raw); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.ToLower(strings.Trim(host, "[]")), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
