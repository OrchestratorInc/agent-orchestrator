package httpd

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	previewutil "github.com/aoagents/agent-orchestrator/backend/internal/preview"
)

func corsMiddleware(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := exactAllowedOrigins(allowedOrigins)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
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

func rendererOriginMiddleware(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := exactAllowedOrigins(allowedOrigins)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if area := rendererOnlyArea(r.URL.Path); origin != "" && area != "" {
				w.Header().Add("Vary", "Origin")
				if _, ok := allowed[origin]; !ok && !mobileAgentAccountsRequest(r, area) {
					envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "ORIGIN_FORBIDDEN",
						"Origin is not allowed to access "+area, nil)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

const agentAccountsArea = "agent accounts"

func mobileAgentAccountsRequest(r *http.Request, area string) bool {
	return area == agentAccountsArea && r.Header.Get("Origin") == "http://localhost" && authenticatedLANBearer(r)
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

func rendererOnlyArea(path string) string {
	if rest, ok := strings.CutPrefix(path, "/api/v1/agents/"); ok {
		parts := strings.Split(rest, "/")
		if len(parts) >= 3 && parts[0] != "" && parts[1] == "auth" && parts[2] == "operations" {
			return "agent credential management"
		}
	}
	for _, area := range []struct {
		name     string
		prefixes []string
	}{
		{"Codex account management", []string{
			"/api/v1/agents/codex/accounts",
			"/api/v1/agents/codex/account-switches",
		}},
		{agentAccountsArea, []string{"/api/v1/agents/accounts"}},
		{"agent credential management", []string{
			"/api/v1/agents/codex/auth/operations",
			"/api/v1/agents/claude-code/auth/operations",
			"/api/v1/agents/provider-keys",
		}},
		{"computer use", []string{
			"/api/v1/computer-use",
			"/api/v1/settings/computer-use",
		}},
	} {
		for _, prefix := range area.prefixes {
			if path == prefix || strings.HasPrefix(path, prefix+"/") {
				return area.name
			}
		}
	}
	return ""
}

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
	if origin == "http://localhost" && authenticatedLANBearer(r) {
		return true
	}
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

func isRenderDocumentRequest(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/api/v1/sessions/")
	parts := strings.Split(rest, "/")
	return ok && len(parts) == 3 && parts[0] != "" && parts[1] == "renders" && parts[2] != "" && parts[2] != "check"
}
