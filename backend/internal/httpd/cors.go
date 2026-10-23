package httpd

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	previewutil "github.com/aoagents/agent-orchestrator/backend/internal/preview"
)

// corsMiddleware grants cross-origin read access to the allowlisted browser
// origins only. The daemon is a no-auth loopback service, so CORS is the one
// boundary between it and hostile browser content running on the same
// machine: the allowlist must never contain "*" or the opaque "null" origin
// (every file:// page and sandboxed iframe on any website presents "null").
// The packaged Electron renderer is served from app://renderer specifically
// so it has a distinct, unforgeable origin this allowlist can name.
//
// Native requests without Origin or browser fetch metadata pass through.
// Requests bearing an Origin outside the allowlist are
// rejected with 403 before any handler runs: merely omitting CORS headers
// would hide the response but NOT the side effect — a hostile page can issue
// "simple" cross-origin POSTs (no-cors mode, text/plain body) that handlers
// would otherwise execute. Same philosophy as localControlRequest on
// /shutdown, applied to the whole surface.
func corsMiddleware(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := exactAllowedOrigins(allowedOrigins)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			// Native clients have no browser metadata. A browser cannot turn
			// an omitted Origin into a native-client grant (e.g. same-origin
			// GET, no-cors subresource, or a DNS-rebinding navigation).
			w.Header().Add("Vary", "Origin")
			w.Header().Add("Vary", "Sec-Fetch-Site")
			if !browserRequestAllowed(r, allowed) {
				writeOriginForbidden(w, r)
				return
			}
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)

			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				h.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
				if reqHeaders := r.Header.Get("Access-Control-Request-Headers"); reqHeaders != "" {
					h.Set("Access-Control-Allow-Headers", reqHeaders)
				}
				h.Set("Access-Control-Max-Age", "600")
				// Chromium's Private Network Access preflight for requests
				// reaching loopback from a less-private address space.
				if r.Header.Get("Access-Control-Request-Private-Network") == "true" {
					h.Set("Access-Control-Allow-Private-Network", "true")
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// codexAccountOriginMiddleware gives credential-management routes a stricter
// browser boundary for credentials, including on authenticated LAN requests.
// Neither previews nor mobile clients may manage these credentials.
// Native clients do not send Origin and remain unauthenticated;
// browser clients must present one of the exact configured renderer origins.
func codexAccountOriginMiddleware(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := exactAllowedOrigins(allowedOrigins)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && isCodexAccountPath(r.URL.Path) {
				w.Header().Add("Vary", "Origin")
				if _, ok := allowed[origin]; !ok {
					envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "ORIGIN_FORBIDDEN",
						"Origin is not allowed to access Codex account management", nil)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func exactAllowedOrigins(origins []string) map[string]struct{} {
	allowed := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		origin = strings.TrimSpace(origin)
		u, err := url.Parse(origin)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "app") || u.Host == "" ||
			u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(origin, " ,\t\r\n") {
			continue
		}
		allowed[origin] = struct{}{}
	}
	return allowed
}

func isCodexAccountPath(path string) bool {
	for _, prefix := range []string{
		"/api/v1/agents/codex/accounts",
		"/api/v1/agents/codex/account-switches",
		"/api/v1/agents/accounts",
	} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

// browserRequestAllowed guards browser access to REST routes. Local HTTP
// origins are not principals: previews and unrelated local servers can serve
// hostile content. Only exact renderer origins grant access to the daemon.
func browserRequestAllowed(r *http.Request, allowed map[string]struct{}) bool {
	origins := r.Header.Values("Origin")
	if len(origins) > 1 || (len(origins) == 1 && origins[0] == "") {
		return false
	}
	origin := r.Header.Get("Origin")
	if previewCookie, _ := r.Context().Value(lanPreviewCookieContextKey{}).(bool); previewCookie && isPreviewFileRequest(r) {
		return true
	}
	if origin == "" {
		return r.Header.Get("Sec-Fetch-Site") == "" || isPreviewHost(r.Host) || isPreviewFileRequest(r) || isRenderDocumentRequest(r) || authenticatedLANBearer(r)
	}
	if _, ok := allowed[origin]; ok {
		return true
	}
	// React Native pins this Origin. It is not trusted on the primary
	// listener, even if someone supplies an Authorization header there.
	if origin == "http://localhost" && authenticatedLANBearer(r) {
		return true
	}
	// A preview may use its own files, never the API or another preview.
	u, err := url.Parse(origin)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.User == nil &&
		u.Path == "" && u.RawQuery == "" && u.Fragment == "" &&
		isPreviewHost(r.Host) && strings.EqualFold(u.Host, r.Host)
}

func writeOriginForbidden(w http.ResponseWriter, r *http.Request) {
	envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "ORIGIN_FORBIDDEN",
		"Origin is not allowed to access this daemon", nil)
}

func isPreviewHost(host string) bool {
	if _, ok := previewutil.SessionIDFromHost(host); ok {
		return true
	}
	if _, ok := previewutil.SessionIDFromArtifactHost(host); ok {
		return true
	}
	_, ok := previewutil.SessionIDFromInlineArtifactHost(host)
	return ok
}

// Only document bytes, not session/control endpoints, may load without Origin
// as browser navigations or sandbox subresources on the legacy preview paths.
func isPreviewFileRequest(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/api/v1/sessions/")
	if !ok {
		return false
	}
	parts := strings.SplitN(rest, "/", 4)
	return len(parts) == 4 && parts[0] != "" && parts[1] == "preview" && (parts[2] == "files" || parts[2] == "artifacts")
}

// Render documents are always served with an opaque-origin CSP sandbox. Allow
// iframe and render-check navigations, never render publication or artifact APIs.
func isRenderDocumentRequest(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/api/v1/sessions/")
	parts := strings.Split(rest, "/")
	return ok && len(parts) == 3 && parts[0] != "" && parts[1] == "renders" && parts[2] != "" && parts[2] != "check"
}
