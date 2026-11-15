package controllers

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

var agentBrowserDownloadGUID = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

func agentBrowserRoute(tail string) bool {
	switch tail {
	case "v/cdp", "ws/cdp", "stats", "upload":
		return true
	}
	guid, ok := strings.CutPrefix(tail, "download/")
	return ok && agentBrowserDownloadGUID.MatchString(guid)
}

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
			for _, name := range []string{"Authorization", "Proxy-Authorization", "Cookie", "Origin", browserCapabilityHeader} {
				req.Out.Header.Del(name)
			}
		},
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
