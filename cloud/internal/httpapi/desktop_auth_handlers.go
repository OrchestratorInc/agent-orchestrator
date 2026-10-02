package httpapi

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"
)

// desktopAuthDeepLink is the only destination the desktop sign-in landing page
// forwards to. It is fixed so the page cannot be used as an open redirect.
const desktopAuthDeepLink = "ao-app://callback"

// desktopAuthForwardedParams are the WorkOS callback parameters the desktop app
// reads. Everything else on the request is dropped.
var desktopAuthForwardedParams = []string{"code", "state", "error", "error_description"}

// desktopAuthCallback is the HTTPS redirect URI for desktop WorkOS sign-in.
// WorkOS redirecting straight to ao-app:// hands the URL to the OS without the
// browser committing a page, so the tab stays frozen on the last provider page
// (for Google, a dimmed account chooser). Landing here first gives the tab a
// real page, which then forwards the callback to the app. The code is useless
// without the PKCE verifier held by the desktop app, so nothing is exchanged or
// stored here.
func (s *Server) desktopAuthCallback(w http.ResponseWriter, r *http.Request) {
	setGitHubCallbackHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	query := r.URL.Query()
	hasCode := strings.TrimSpace(query.Get("code")) != "" && strings.TrimSpace(query.Get("state")) != ""
	hasError := strings.TrimSpace(query.Get("error")) != ""
	if !hasCode && !hasError {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write(renderDesktopAuthHTML(
			"Sign-in link is incomplete",
			"Return to Agent Orchestrator and try signing in again.",
			"",
		))
		return
	}
	forwarded := url.Values{}
	for _, name := range desktopAuthForwardedParams {
		if value := query.Get(name); value != "" {
			forwarded.Set(name, value)
		}
	}
	deepLink := desktopAuthDeepLink + "?" + forwarded.Encode()
	title := "Finishing sign-in"
	message := "Agent Orchestrator will open to complete sign-in. You can close this tab once it does."
	if hasError {
		title = "Sign-in did not complete"
		message = "Return to Agent Orchestrator and try signing in again."
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(renderDesktopAuthHTML(title, message, deepLink))
}

// renderDesktopAuthHTML mirrors the plain GitHub callback page. When deepLink
// is set the page forwards to it immediately and offers a button for browsers
// that hold the protocol hand-off behind a click.
func renderDesktopAuthHTML(title, message, deepLink string) []byte {
	refresh, action := "", ""
	if deepLink != "" {
		escaped := html.EscapeString(deepLink)
		refresh = fmt.Sprintf(`<meta http-equiv="refresh" content="0;url=%s">`, escaped)
		action = fmt.Sprintf(`<p><a href="%s" style="display:inline-block;padding:.5rem 1rem;border-radius:6px;background:#111;color:#fff;text-decoration:none">Open Agent Orchestrator</a></p>`, escaped)
	}
	return []byte(fmt.Sprintf(
		`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">%s<title>%s</title>
<body style="font:15px -apple-system,system-ui,sans-serif;max-width:32rem;margin:15vh auto;padding:0 1.5rem;color:#111">
<main><h1 style="font-size:1.25rem">%s</h1><p style="color:#555">%s</p>%s</main></body></html>`,
		refresh, html.EscapeString(title), html.EscapeString(title), html.EscapeString(message), action))
}
