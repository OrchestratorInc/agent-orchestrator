package controllers

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

// agentBrowserDownloadGUID matches the browser's download GUIDs. Anything else,
// "." and ".." above all, reaches a path the browser's mux cleans with a
// redirect that repeats the query, token included.
var agentBrowserDownloadGUID = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// agentBrowserRoute reports whether the desktop may reach this agent browser
// path: the stream viewer's page, its socket, and its file transfers. The
// agent's own command route and the browser's debugging routes stay on the
// host.
func agentBrowserRoute(tail string) bool {
	switch tail {
	case "v/cdp", "ws/cdp", "stats", "upload":
		return true
	}
	guid, ok := strings.CutPrefix(tail, "download/")
	return ok && agentBrowserDownloadGUID.MatchString(guid)
}

// agentBrowser streams the host's agent browser (AO_AGENT_BROWSER_URL) to the
// desktop as the session's agent tab, over the daemon's own authenticated
// connection. The browser's token is added here: clients never hold it, and
// the browser's port never leaves the host.
//
// Keep agentBrowserRoute, the headers dropped and the Location scrub in
// lockstep with internal/agentbrowser/agentbrowser.go in the private AO Cloud
// control plane (OrchestratorInc/ao-control-plane), its copy:
// the cloud image builds against a pinned backend, so they cannot share code.
// ponytail: one agent browser per host, shown in each of its sessions; route per session when sessions get their own browser.
func (c *SessionsController) agentBrowser(w http.ResponseWriter, r *http.Request) {
	if c.AgentBrowser == nil {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "AGENT_BROWSER_NOT_FOUND", "This host has no agent browser", nil)
		return
	}
	if _, err := c.Svc.GetRecord(r.Context(), sessionID(r)); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	tail, _, err := wildcardPath(r)
	if err != nil || !agentBrowserRoute(tail) {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "AGENT_BROWSER_NOT_FOUND", "Not an agent browser route", nil)
		return
	}
	target, token := c.AgentBrowser, c.AgentBrowserToken
	proxy := &httputil.ReverseProxy{
		Transport: previewTransport,
		Rewrite: func(req *httputil.ProxyRequest) {
			req.SetURL(target)
			req.Out.Host = target.Host
			req.Out.URL.Path = strings.TrimSuffix(target.Path, "/") + "/" + tail
			req.Out.URL.RawPath = ""
			query := req.In.URL.Query()
			query.Set("token", token)
			req.Out.URL.RawQuery = query.Encode()
			// AO's credential and cookies are for the daemon. The desktop's
			// preview origin is not the browser's, whose socket check would
			// refuse the stream: the request arrives as the host's own.
			for _, name := range []string{"Authorization", "Proxy-Authorization", "Cookie", "Origin", browserCapabilityHeader} {
				req.Out.Header.Del(name)
			}
		},
		// A redirect repeats the request's query, the browser's token with it:
		// keep the token out of the Location and drop the body, which links it.
		ModifyResponse: func(res *http.Response) error {
			if location, parseErr := url.Parse(res.Header.Get("Location")); parseErr == nil && location.Query().Has("token") {
				query := location.Query()
				query.Del("token")
				location.RawQuery = query.Encode()
				res.Header.Set("Location", location.String())
				_ = res.Body.Close()
				res.Body = http.NoBody
				res.ContentLength = 0
				res.Header.Del("Content-Length")
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "Agent browser unavailable", http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r)
}
